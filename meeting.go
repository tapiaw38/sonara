package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Meeting transcription records two tracks at once (the chosen microphone and
// the meeting application's output) on one shared clock, cuts each into blocks,
// transcribes them in the background and stores the text as it arrives. It is
// deliberately separate from the copilot's listenMeetAudio: that loop answers
// questions and may drop audio under load, this one must not lose a stretch of
// the meeting without saying so.

const (
	meetingFrameMS = 100 // one meetFrameBytes frame
	// A block is cut at a pause once it is at least 10s long, at any quiet frame
	// once it reaches 20s, and unconditionally at 30s. All of it sits far under
	// DeepSeek's 120s / 8 MiB limit per request.
	meetingMinFrames   = 100
	meetingSoftFrames  = 200
	meetingHardFrames  = 300
	meetingQuietFrames = 4
	// meetingQueue bounds blocks waiting for the network (about 20 minutes of
	// backlog). A full queue records a visible gap; it never drops silently.
	meetingQueue   = 64
	meetingWorkers = 2
	meetingRetries = 2
	// meetingGapMS is how long a track may be uncaptured before the hole is
	// written down: shorter than that is just the recorder restarting.
	meetingGapMS = 1500
	// meetingKeptFailed caps the failed blocks kept in memory for retry (~13 MB).
	meetingKeptFailed = 20
)

const (
	meetingIdle       = "idle"
	meetingRecording  = "recording"
	meetingProcessing = "processing" // capture stopped, blocks still being transcribed
)

// retryDelay is a variable so tests do not sleep.
var retryDelay = func(attempt int) time.Duration { return time.Duration(attempt) * 2 * time.Second }

type meetingBlock struct {
	track   string
	startMS int64
	endMS   int64
	pcm     []byte
	// voiced is false for a block that never rose above the ambient level:
	// uploading silence would only cost money.
	voiced bool
}

// blockCutter turns a stream of 100ms frames into transcribable blocks.
type blockCutter struct {
	track   string
	pcm     []byte
	startMS int64
	frames  int
	voiced  int
	quiet   int
	ambient noiseFloor
}

func (c *blockCutter) add(frame []byte, frameStartMS int64) (meetingBlock, bool) {
	level := pcmRMS(frame)
	voice := level >= c.ambient.threshold()
	if !voice {
		c.ambient.observe(level)
	}
	if c.frames == 0 {
		c.startMS = frameStartMS
	}
	c.pcm = append(c.pcm, frame...)
	c.frames++
	if voice {
		c.voiced++
		c.quiet = 0
	} else {
		c.quiet++
	}
	cut := c.frames >= meetingHardFrames ||
		(c.frames >= meetingSoftFrames && c.quiet >= 1) ||
		(c.frames >= meetingMinFrames && c.quiet >= meetingQuietFrames)
	if !cut {
		return meetingBlock{}, false
	}
	return c.flush()
}

// flush returns whatever is buffered as a block, or false when nothing is.
func (c *blockCutter) flush() (meetingBlock, bool) {
	if c.frames == 0 {
		return meetingBlock{}, false
	}
	block := meetingBlock{
		track: c.track, startMS: c.startMS, endMS: c.startMS + int64(c.frames)*meetingFrameMS,
		pcm: c.pcm, voiced: c.voiced >= meetVoiceFrames,
	}
	c.pcm, c.frames, c.voiced, c.quiet = nil, 0, 0, 0
	return block, true
}

type meetingRun struct {
	sessionID string
	started   time.Time
	language  string
	cancel    context.CancelFunc
	jobs      chan meetingBlock
	recorders sync.WaitGroup
	workers   sync.WaitGroup
	// done closes once every block has been transcribed and stored.
	done chan struct{}

	mu          sync.Mutex
	state       string
	tracks      map[string]string
	message     string
	lastEnd     map[string]int64
	pending     int
	failed      int
	failedBlock map[int64]meetingBlock
	failedOrder []int64

	// Phase 2 (diarization): the meeting_output track's raw audio, held only
	// while diarization is enabled and only until the final pass consumes it.
	// See diarize.go.
	diarizeMu     sync.Mutex
	diarizePCM    []byte
	diarizeCapped bool
}

func (r *meetingRun) elapsedMS() int64 { return time.Since(r.started).Milliseconds() }

func (r *meetingRun) setTrack(track, state string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tracks[track] == state {
		return false
	}
	r.tracks[track] = state
	return true
}

func (r *meetingRun) keepFailed(id int64, block meetingBlock) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed++
	r.failedBlock[id] = block
	r.failedOrder = append(r.failedOrder, id)
	for len(r.failedOrder) > meetingKeptFailed {
		delete(r.failedBlock, r.failedOrder[0])
		r.failedOrder = r.failedOrder[1:]
	}
}

// MeetingStatus is what the UI shows while a meeting is recorded or processed.
type MeetingStatus struct {
	State           string `json:"state"`
	SessionID       string `json:"sessionId"`
	ElapsedMS       int64  `json:"elapsedMs"`
	Provider        string `json:"provider"`
	MicrophoneState string `json:"microphoneState"`
	OutputState     string `json:"outputState"`
	Pending         int    `json:"pending"`
	Failed          int    `json:"failed"`
	Message         string `json:"message"`
	// DiarizeEnabled reflects whether a Gemini key is configured, not whether
	// the call will actually succeed: that is only known after a meeting ends.
	DiarizeEnabled bool `json:"diarizeEnabled"`
}

// MeetingSegmentEvent is the payload of the "meeting:segment" event.
type MeetingSegmentEvent struct {
	SessionID string            `json:"sessionId"`
	Segment   TranscriptSegment `json:"segment"`
}

func (a *App) emit(event string, data any) {
	a.mu.RLock()
	ctx, hook := a.ctx, a.emitHook
	a.mu.RUnlock()
	if hook != nil {
		hook(event, data)
		return
	}
	if ctx != nil {
		wruntime.EventsEmit(ctx, event, data)
	}
}

func (a *App) emitMeetingStatus() { a.emit("meeting:status", a.MeetingTranscriptStatus()) }

func (a *App) MeetingTranscriptStatus() MeetingStatus {
	a.mu.RLock()
	run, backend := a.meeting, a.backend
	a.mu.RUnlock()
	status := MeetingStatus{State: meetingIdle, DiarizeEnabled: a.diarizer.Configured()}
	if backend != nil {
		status.Provider = backend.MeetingProvider()
	}
	if run == nil {
		return status
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	status.SessionID = run.sessionID
	if run.state != "" {
		status.State = run.state
	}
	if status.State == meetingIdle {
		return status
	}
	status.ElapsedMS = run.elapsedMS()
	status.MicrophoneState, status.OutputState = run.tracks[trackMicrophone], run.tracks[trackOutput]
	status.Pending, status.Failed, status.Message = run.pending, run.failed, run.message
	return status
}

// SelectedMicrophone is the remembered microphone's node name ("" when none).
func (a *App) SelectedMicrophone() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.preferredMic
}

// SetMicrophone remembers a microphone by its stable node name. An empty target
// clears the choice. Recording never falls back to another device: a missing
// microphone pauses its track instead of capturing something unexpected.
func (a *App) SetMicrophone(target string) (string, error) {
	name := ""
	if target != "" {
		sources, err := a.Microphones()
		if err != nil {
			return "", err
		}
		name, err = findMicrophoneApp(sources, target)
		if err != nil {
			return "", err
		}
	}
	a.mu.Lock()
	a.preferredMic = name
	database := a.store
	a.mu.Unlock()
	if database != nil {
		if err := database.setSetting(settingMicrophone, name); err != nil {
			logf("could not persist the microphone: %v", err)
		}
	}
	return name, nil
}

// findMicrophoneApp looks up target (the stable node name, AudioSource.App)
// among the microphones PipeWire currently reports. target is never the
// ephemeral AudioSource.Target serial — that gets recreated on every boot or
// reconnect, so a match on it would never stick; the UI's <option> sends App
// for exactly this reason.
func findMicrophoneApp(sources []AudioSource, target string) (string, error) {
	for _, source := range sources {
		if source.App == target {
			return source.App, nil
		}
	}
	return "", fmt.Errorf("el micrófono ya no está disponible")
}

// StartMeetingTranscript begins recording a new meeting. It never touches the
// copilot session: the meeting is its own session, and the copilot can keep
// listening (or not) independently.
func (a *App) StartMeetingTranscript() (MeetingStatus, error) {
	a.mu.Lock()
	if a.meeting != nil && a.meeting.stateNow() != meetingIdle {
		a.mu.Unlock()
		if a.meeting.stateNow() == meetingProcessing {
			return a.MeetingTranscriptStatus(), fmt.Errorf("todavía se está procesando la reunión anterior")
		}
		return a.MeetingTranscriptStatus(), fmt.Errorf("ya hay una reunión grabándose")
	}
	database, backend, mic, language := a.store, a.backend, a.preferredMic, a.audioLanguage
	parent := a.ctx
	a.mu.Unlock()
	switch {
	case database == nil:
		return MeetingStatus{}, fmt.Errorf("sin persistencia: la transcripción no se podría guardar")
	case mic == "":
		return MeetingStatus{}, fmt.Errorf("elegí un micrófono antes de grabar")
	case backend == nil:
		return MeetingStatus{}, fmt.Errorf("backend no inicializado")
	}
	if parent == nil {
		parent = context.Background()
	}

	started := time.Now()
	sessionID := newSessionID()
	if err := database.saveSession(sessionID, meetingMode, language); err != nil {
		return MeetingStatus{}, fmt.Errorf("no se pudo crear la sesión: %w", err)
	}
	// A title up front keeps the session list readable and stops the copilot's
	// model-based titling from ever running on a meeting.
	if err := database.saveTitle(sessionID, "Reunión · "+started.Format("2006-01-02 15:04")); err != nil {
		logf("could not title meeting %s: %v", sessionID, err)
	}

	ctx, cancel := context.WithCancel(parent)
	run := &meetingRun{
		sessionID: sessionID, started: started, language: language, cancel: cancel,
		jobs: make(chan meetingBlock, meetingQueue), done: make(chan struct{}),
		state:  meetingRecording,
		tracks: map[string]string{trackMicrophone: "starting", trackOutput: "starting"}, lastEnd: map[string]int64{},
		failedBlock: map[int64]meetingBlock{},
	}
	a.mu.Lock()
	a.meeting = run
	a.mu.Unlock()
	for i := 0; i < meetingWorkers; i++ {
		run.workers.Add(1)
		go a.meetingWorker(run)
	}
	for _, track := range []string{trackMicrophone, trackOutput} {
		run.recorders.Add(1)
		go a.recordTrack(ctx, run, track)
	}
	logf("meeting transcript %s started (provider %s)", sessionID, backend.MeetingProvider())
	a.emitMeetingStatus()
	return a.MeetingTranscriptStatus(), nil
}

func (r *meetingRun) stateNow() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// StopMeetingTranscript ends capture. Blocks already recorded keep being
// transcribed ("processing"); the status event announces when it is all stored.
func (a *App) StopMeetingTranscript() MeetingStatus {
	a.mu.RLock()
	run := a.meeting
	a.mu.RUnlock()
	if run == nil {
		return a.MeetingTranscriptStatus()
	}
	run.mu.Lock()
	if run.state != meetingRecording {
		run.mu.Unlock()
		return a.MeetingTranscriptStatus()
	}
	run.state = meetingProcessing
	run.mu.Unlock()
	run.cancel()
	go a.finishMeeting(run)
	a.emitMeetingStatus()
	return a.MeetingTranscriptStatus()
}

// recordTrailingGaps writes down the end of a recording a track did not cover:
// a track that never started, or whose device vanished and never came back,
// would otherwise leave no trace in the transcript.
func (a *App) recordTrailingGaps(run *meetingRun) {
	end := run.elapsedMS()
	for _, track := range []string{trackMicrophone, trackOutput} {
		run.mu.Lock()
		lastEnd := run.lastEnd[track]
		run.mu.Unlock()
		if end-lastEnd > meetingGapMS {
			a.recordGap(run, track, lastEnd, end)
		}
	}
}

func (a *App) finishMeeting(run *meetingRun) {
	run.recorders.Wait()
	a.recordTrailingGaps(run)
	close(run.jobs)
	run.workers.Wait()
	// Both best-effort and, for a long meeting, possibly slow: "processing"
	// covers it, and shutdown's own bound (stopMeetingAndWait) still applies
	// on top. runVoiceMatch needs the same buffer runDiarization read, so the
	// release only happens once both have had their chance at it.
	a.runDiarization(run)
	a.runVoiceMatch(run)
	run.releaseDiarizePCM()
	run.mu.Lock()
	run.state = meetingIdle
	run.mu.Unlock()
	close(run.done)
	logf("meeting transcript %s finished", run.sessionID)
	a.emitMeetingStatus()
}

// stopMeetingAndWait is for shutdown: the store closes right after, so pending
// blocks must be stored first. Bounded, because a hung request must not keep
// the window from closing.
func (a *App) stopMeetingAndWait(limit time.Duration) {
	a.mu.RLock()
	run := a.meeting
	a.mu.RUnlock()
	if run == nil {
		return
	}
	a.StopMeetingTranscript()
	select {
	case <-run.done:
	case <-time.After(limit):
		logf("meeting transcript %s: shutdown did not wait for pending blocks", run.sessionID)
	}
}

func (a *App) meetingTarget(ctx context.Context, track string) (string, error) {
	if track == trackOutput {
		return a.audioTarget(ctx)
	}
	a.mu.RLock()
	want := a.preferredMic
	a.mu.RUnlock()
	sources, err := availableMicrophones(ctx)
	if err != nil {
		return "", err
	}
	for _, source := range sources {
		if source.App == want {
			return source.Target, nil
		}
	}
	return "", fmt.Errorf("el micrófono elegido no está disponible")
}

func (a *App) recordTrack(ctx context.Context, run *meetingRun, track string) {
	defer run.recorders.Done()
	for ctx.Err() == nil {
		target, err := a.meetingTarget(ctx, track)
		if err != nil {
			logDedup("meeting-"+track, "meeting track %s unavailable: %v", track, err)
			if run.setTrack(track, "missing") {
				a.emitMeetingStatus()
			}
			if !waitForRetry(ctx) {
				return
			}
			continue
		}
		a.captureTrack(ctx, run, track, target)
		if ctx.Err() != nil {
			return
		}
		if run.setTrack(track, "reconnecting") {
			a.emitMeetingStatus()
		}
		if !waitForRetry(ctx) {
			return
		}
	}
}

// captureTrack runs one pw-record until it ends, then flushes what it had so no
// audio is lost when the device vanishes or the user stops.
func (a *App) captureTrack(ctx context.Context, run *meetingRun, track, target string) {
	capCtx, stop := context.WithCancel(ctx)
	command := exec.CommandContext(capCtx, "pw-record", "--target", target, "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	stdout, err := command.StdoutPipe()
	if err != nil || command.Start() != nil {
		stop()
		logDedup("meeting-"+track, "pw-record could not attach to %s: %v", target, err)
		if run.setTrack(track, "error") {
			a.emitMeetingStatus()
		}
		return
	}
	defer func() { stop(); command.Wait() }()
	if track == trackMicrophone {
		go a.watchMicrophone(capCtx, stop)
	}
	logDedup("meeting-"+track, "meeting track %s capturing %s", track, target)

	// Offsets come from counting samples, not from wall-clock reads: a late read
	// then cannot shift text. The wall clock only anchors where a recorder
	// (re)starts on the shared timeline.
	origin := run.elapsedMS()
	run.mu.Lock()
	lastEnd := run.lastEnd[track]
	run.mu.Unlock()
	if origin-lastEnd > meetingGapMS {
		a.recordGap(run, track, lastEnd, origin)
	}
	if run.setTrack(track, "ok") {
		a.emitMeetingStatus()
	}
	cutter := &blockCutter{track: track}
	frame := make([]byte, meetFrameBytes)
	read := int64(0)
	for {
		if _, err := io.ReadFull(stdout, frame); err != nil {
			break
		}
		if track == trackOutput && a.diarizer.Configured() {
			// The raw, continuous signal, not VAD-gated: the model does its own
			// segmentation and needs the real pauses to find speaker turns.
			run.appendDiarizePCM(frame)
		}
		block, ready := cutter.add(frame, origin+read*meetingFrameMS)
		read++
		if ready {
			a.enqueueBlock(run, block)
		}
	}
	if block, ready := cutter.flush(); ready {
		a.enqueueBlock(run, block)
	}
	run.mu.Lock()
	run.lastEnd[track] = origin + read*meetingFrameMS
	run.mu.Unlock()
}

// watchMicrophone stops a capture whose device was unplugged: pw-record can
// outlive its node and keep returning silence, which would look like a quiet
// meeting instead of a missing microphone.
func (a *App) watchMicrophone(ctx context.Context, stop context.CancelFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		if _, err := a.meetingTarget(ctx, trackMicrophone); err != nil && ctx.Err() == nil {
			logDedup("meeting-"+trackMicrophone, "microphone vanished; stopping its recorder")
			stop()
			return
		}
	}
}

func (a *App) recordGap(run *meetingRun, track string, from, to int64) {
	a.storeSegment(run, TranscriptSegment{Track: track, StartMS: from, EndMS: to, Status: segmentGap})
	if track == trackOutput && a.diarizer.Configured() {
		// Keeps the accumulator's byte offset aligned to the shared clock
		// across the gap, including the very first one before capture starts.
		run.padDiarizeSilence(to - from)
	}
}

// enqueueBlock never blocks the recorder. A full queue becomes a visible gap.
func (a *App) enqueueBlock(run *meetingRun, block meetingBlock) {
	if !block.voiced {
		return
	}
	run.mu.Lock()
	run.pending++
	run.mu.Unlock()
	select {
	case run.jobs <- block:
	default:
		run.mu.Lock()
		run.pending--
		run.message = "Cola llena: se perdió audio, queda marcado en la transcripción"
		run.mu.Unlock()
		a.recordGap(run, block.track, block.startMS, block.endMS)
	}
}

func (a *App) meetingWorker(run *meetingRun) {
	defer run.workers.Done()
	for block := range run.jobs {
		a.processBlock(run, block)
	}
}

// transcribeMeetingBlock sends one block, retrying transient failures. The
// router has already tried the fallback provider by the time an error returns.
func (a *App) transcribeMeetingBlock(run *meetingRun, pcm []byte) (string, error) {
	var err error
	for attempt := 0; attempt <= meetingRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(retryDelay(attempt))
		}
		var text string
		if text, err = a.transcribeOnce(run.language, pcm); err == nil {
			return text, nil
		}
	}
	return "", err
}

func (a *App) transcribeOnce(language string, pcm []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	a.mu.RLock()
	hook, backend := a.meetingTranscribe, a.backend
	a.mu.RUnlock()
	wav := wavFromPCM(pcm)
	if hook != nil {
		return hook(ctx, wav, language)
	}
	if backend == nil {
		return "", fmt.Errorf("backend no inicializado")
	}
	out, err := backend.TranscribeMeeting(ctx, wav, "meeting.wav", language)
	return out.Text, err
}

// processBlock transcribes one block and stores the result before anything
// else can fail: text that arrived is never lost to a later error.
func (a *App) processBlock(run *meetingRun, block meetingBlock) {
	text, err := a.transcribeMeetingBlock(run, block.pcm)
	run.mu.Lock()
	run.pending--
	run.mu.Unlock()
	segment := TranscriptSegment{Track: block.track, StartMS: block.startMS, EndMS: block.endMS, Status: segmentOK}
	if err != nil {
		// No content in the log: only the fact and the span that is missing.
		logf("meeting %s: block %s %d-%dms failed: %v", run.sessionID, block.track, block.startMS, block.endMS, err)
		segment.Status = segmentFailed
	} else if segment.Text = strings.TrimSpace(text); segment.Text == "" {
		a.emitMeetingStatus()
		return
	}
	id := a.storeSegment(run, segment)
	if segment.Status == segmentFailed && id != 0 {
		run.keepFailed(id, block)
	}
	a.emitMeetingStatus()
}

// storeSegment persists a segment and tells the UI. It returns the row id, or 0
// when the write failed (reported in the status line, never swallowed).
func (a *App) storeSegment(run *meetingRun, segment TranscriptSegment) int64 {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return 0
	}
	id, err := database.insertSegment(run.sessionID, segment)
	if err != nil {
		logf("meeting %s: could not store a segment: %v", run.sessionID, err)
		run.mu.Lock()
		run.message = "No se pudo guardar un fragmento de la transcripción"
		run.mu.Unlock()
		return 0
	}
	segment.ID = id
	a.emit("meeting:segment", MeetingSegmentEvent{SessionID: run.sessionID, Segment: segment})
	return id
}

// RetryMeetingSegments re-sends failed blocks whose audio is still in memory
// (the last few of the current or latest recording). Audio is never written to
// disk, so a block lost across a restart cannot be retried.
func (a *App) RetryMeetingSegments(sessionID string) (int, error) {
	a.mu.RLock()
	run, database := a.meeting, a.store
	a.mu.RUnlock()
	if run == nil || run.sessionID != sessionID || database == nil {
		return 0, fmt.Errorf("el audio de esa reunión ya no está en memoria")
	}
	run.mu.Lock()
	ids := append([]int64(nil), run.failedOrder...)
	run.mu.Unlock()
	retried := 0
	for _, id := range ids {
		run.mu.Lock()
		block, ok := run.failedBlock[id]
		run.mu.Unlock()
		if !ok {
			continue
		}
		text, err := a.transcribeMeetingBlock(run, block.pcm)
		if err != nil {
			continue
		}
		text = strings.TrimSpace(text)
		status := segmentOK
		if text == "" {
			status = segmentGap
		}
		if err := database.updateSegment(id, text, status); err != nil {
			continue
		}
		run.mu.Lock()
		delete(run.failedBlock, id)
		for i, kept := range run.failedOrder {
			if kept == id {
				run.failedOrder = append(run.failedOrder[:i], run.failedOrder[i+1:]...)
				break
			}
		}
		run.failed--
		run.mu.Unlock()
		retried++
		a.emit("meeting:segment", MeetingSegmentEvent{SessionID: sessionID, Segment: TranscriptSegment{
			ID: id, Track: block.track, StartMS: block.startMS, EndMS: block.endMS, Text: text, Status: status,
		}})
	}
	a.emitMeetingStatus()
	return retried, nil
}

// MeetingView is a stored meeting as the UI shows it.
type MeetingView struct {
	SessionID string              `json:"sessionId"`
	StartedAt string              `json:"startedAt"`
	Language  string              `json:"language"`
	Segments  []TranscriptSegment `json:"segments"`
	Speakers  map[string]string   `json:"speakers"`
}

func (a *App) MeetingTranscriptSegments(sessionID string) (MeetingView, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return MeetingView{}, fmt.Errorf("sin persistencia disponible")
	}
	startedAt, mode, language, err := database.sessionInfo(sessionID)
	if err != nil {
		return MeetingView{}, err
	}
	if mode != meetingMode {
		return MeetingView{}, fmt.Errorf("la sesión %s no es una reunión", sessionID)
	}
	segments, err := database.transcriptSegments(sessionID)
	if err != nil {
		return MeetingView{}, err
	}
	speakers, err := database.speakerNames(sessionID)
	if err != nil {
		return MeetingView{}, err
	}
	return MeetingView{SessionID: sessionID, StartedAt: startedAt, Language: language, Segments: segments, Speakers: speakers}, nil
}

// RenameMeetingSpeaker sets the display name of a track (speakerKey "") or of
// one diarized participant within the meeting_output track (speakerKey
// "spk_N", as returned in a segment's speakerKey). An empty name restores the
// default ("Yo", "Participante", or "Participante N").
func (a *App) RenameMeetingSpeaker(sessionID, track, speakerKey, name string) (map[string]string, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return nil, fmt.Errorf("sin persistencia disponible")
	}
	if err := database.saveSpeakerName(sessionID, track, speakerKey, name); err != nil {
		return nil, err
	}
	return database.speakerNames(sessionID)
}

// meetingTranscriber is the test seam for the network call.
type meetingTranscriber = func(ctx context.Context, wav []byte, language string) (string, error)

// meetingInProgress reports whether id is a meeting still being recorded or
// processed: its rows are being written, so it cannot be deleted.
func (a *App) meetingInProgress(id string) bool {
	a.mu.RLock()
	run := a.meeting
	a.mu.RUnlock()
	return run != nil && run.sessionID == id && run.stateNow() != meetingIdle
}
