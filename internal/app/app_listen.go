package app

import (
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sync/atomic"
	"time"

	sonaraBackend "sonara/backend/client"
	"sonara/internal/audio"
)

type meetSegment struct {
	pcm      []byte
	session  uint64
	turn     uint64
	part     uint64
	language string
	manual   bool
	final    bool
	// queuedAt is when the recorder handed the segment over; set by
	// queueMeetSegment, used only to time voice end -> first token.
	queuedAt time.Time
}

type ListeningState struct {
	Listening       bool   `json:"listening"`
	Transcript      string `json:"transcript"`
	TranscriptFinal bool   `json:"transcriptFinal"`
	// Partial: Transcript is still being spoken (streaming), not final yet.
	Partial             bool                            `json:"partial"`
	Status              string                          `json:"status"`
	Response            string                          `json:"response"`
	ResponseTranslation string                          `json:"responseTranslation"`
	ResponseLines       []sonaraBackend.TranslationLine `json:"responseLines"`
	Sequence            uint64                          `json:"sequence"`
	ManualSend          bool                            `json:"manualSend"`
	PendingSeconds      float64                         `json:"pendingSeconds"`
	Streaming           bool                            `json:"streaming"`
	Language            string                          `json:"language"`
	Translation         string                          `json:"translation"`
	TranslationLines    []sonaraBackend.TranslationLine `json:"translationLines"`
	DroppedSegments     uint64                          `json:"droppedSegments"`
	AudioSource         string                          `json:"audioSource"`
	SessionID           string                          `json:"sessionId"`
	AudioGuessing       bool                            `json:"audioGuessing"`
	AudioCandidates     int                             `json:"audioCandidates"`
	// Retractions counts replies taken back (anticipated replies that did not
	// hold); the UI removes the message it started for each.
	Retractions uint64 `json:"retractions"`
	LiveMode    string `json:"liveMode"`
}

// Greet returns a greeting for the given name
func (a *App) StartListening() bool {
	a.segmentOnce.Do(func() { go a.runSegmentWorker() })
	a.mu.Lock()
	if a.listening {
		a.mu.Unlock()
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.listening = true
	a.listenerCancel = cancel
	a.listenStatus = "Conectando audio de la reunión…"
	a.transcript = "Esperando intervención…"
	a.transcriptFinal = false
	a.mu.Unlock()
	go a.listenMeetAudio(ctx)
	go a.listenMicForContext(ctx)
	return true
}

func (a *App) StopListening() bool {
	a.mu.Lock()
	if a.listenerCancel != nil {
		a.listenerCancel()
		a.listenerCancel = nil
	}
	a.listening = false
	a.listenStatus = "Escucha pausada"
	a.audioLevel = 0
	a.mu.Unlock()
	return false
}

func (a *App) ListeningStatus() ListeningState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return ListeningState{
		Listening: a.listening, Transcript: a.transcript, TranscriptFinal: a.transcriptFinal, Status: a.listenStatus,
		Partial:  a.livePartial != "" && a.livePartial == a.transcript,
		Response: a.voiceReply, Sequence: a.voiceSequence,
		ResponseTranslation: a.voiceReplyTranslation, ResponseLines: a.voiceReplyLines,
		ManualSend:       a.manualSend,
		PendingSeconds:   float64(len(a.pendingAudio)) / float64(audio.SampleRate*2),
		Streaming:        a.streaming,
		Language:         a.audioLanguage,
		Translation:      a.translation,
		TranslationLines: a.translationLines,
		Retractions:      a.voiceRetractions,
		LiveMode:         liveModeOf(a.liveStreaming),
		DroppedSegments:  a.droppedSegments,
		AudioSource:      a.selectedAudioSource,
		SessionID:        a.sessionID,
		AudioGuessing:    a.audioGuessing,
		AudioCandidates:  a.audioCandidates,
	}
}

// SetManualSend switches between sending every detected segment automatically
// and holding the last one until the user presses send.
func (a *App) SetManualSend(manual bool) bool {
	a.mu.Lock()
	a.manualSend = manual
	if manual {
		a.listenStatus = "Modo manual: los fragmentos quedan en espera"
	} else {
		a.listenStatus = "Modo automático: se envía al detectar silencio"
		a.pendingAudio = nil
	}
	a.mu.Unlock()
	return manual
}

// SendPendingSegment sends the segment held in manual mode.
func (a *App) SendPendingSegment() bool {
	a.segmentOnce.Do(func() { go a.runSegmentWorker() })
	a.mu.Lock()
	if len(a.pendingAudio) == 0 {
		a.mu.Unlock()
		return false
	}
	a.turnSequence++
	language := a.audioLanguage
	if language == "" {
		language = "es"
	}
	item := meetSegment{pcm: a.pendingAudio, session: a.sessionGeneration, turn: a.turnSequence, part: 1, language: language, final: true}
	if !queueSegment(a.segments, item) {
		a.listenStatus = "Cola llena; reintentá enviar el fragmento"
		a.mu.Unlock()
		return false
	}
	a.pendingAudio = nil
	a.mu.Unlock()
	return true
}

func (a *App) DiscardPendingSegment() bool {
	a.mu.Lock()
	a.pendingAudio = nil
	a.listenStatus = "Fragmento descartado"
	a.mu.Unlock()
	return true
}

// SetAudioLanguage records the language spoken in the call.
func (a *App) SetAudioLanguage(language string) string {
	if language != "en" {
		language = "es"
	}
	a.mu.Lock()
	a.audioLanguage = language
	a.mu.Unlock()
	return language
}

// AudioLevel reports the RMS of the last captured 100ms frame, normalised so
// the UI can draw the real signal instead of a decorative animation. It is
// polled far more often than ListeningStatus, so it stays a bare float.
func (a *App) AudioLevel() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return math.Min(1, a.audioLevel/meetLoudFrame)
}

func (a *App) setAudioLevel(level float64) {
	a.mu.Lock()
	a.audioLevel = level
	a.mu.Unlock()
}

func (a *App) IsListening() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.listening
}

const (
	// 500ms of quiet ends an intervention. This is the single biggest term in
	// time-to-answer, and it matches what OpenAI's server VAD uses as its own
	// silence_duration_ms default, so it is short enough not to split sentences.
	meetSilenceFrames = 5
	meetMinimumFrames = 10  // ignore anything shorter than a second
	meetMaximumFrames = 150 // flush after 15s of continuous speech
	meetVoiceFrames   = 5   // a segment needs half a second of actual voice
	meetStartFrames   = 3   // 300ms of continuous voice starts recording
	meetKeepSilence   = 3   // trailing frames kept so words are not clipped
	// meetOverlapFrames is replayed at the head of a continuation chunk so a
	// word straddling the 15s flush is transcribed whole by one of the two.
	meetOverlapFrames = 2
	// meetMaxParts bounds one intervention. Each part is 15s, so this allows a
	// question up to a minute; past that the stream is not someone talking, and
	// continuing would transcribe wall-clock time for as long as it plays.
	meetMaxParts = 4
	// meetQueuedSegments is how many finished segments may wait for the network.
	// The capture loop must never block on a transcription: pw-record's pipe only
	// holds ~2s, so a blocked reader goes deaf for the rest of the question.
	meetQueuedSegments = 4
	meetLoudFrame      = 0.08 // RMS treated as a full-height bar in the UI
)

// queueSegment never blocks the recorder. The copy detaches reused PCM buffers.
func queueSegment(segments chan<- meetSegment, item meetSegment) bool {
	item.pcm = append([]byte(nil), item.pcm...)
	select {
	case segments <- item:
		return true
	default:
		return false
	}
}

func (a *App) nextMeetTurn() meetSegment {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.turnSequence++
	language := a.audioLanguage
	if language == "" {
		language = "es"
	}
	return meetSegment{session: a.sessionGeneration, turn: a.turnSequence, language: language, manual: a.manualSend}
}

func (a *App) queueMeetSegment(item meetSegment) bool {
	item.queuedAt = time.Now()
	if queueSegment(a.segments, item) {
		return true
	}
	a.mu.Lock()
	a.droppedSegments++
	dropped := a.droppedSegments
	a.listenStatus = fmt.Sprintf("Audio perdido: cola llena (%d)", dropped)
	a.mu.Unlock()
	// The network is slower than the conversation. This is the one failure that
	// silently costs the user an answer, so it is always worth a line.
	logf("segment dropped: queue full at %d pending (turn %d/%d part %d, total dropped %d)",
		meetQueuedSegments, item.session, item.turn, item.part, dropped)
	return false
}

// segmentClose decides how a segment ends. A real pause closes the turn. A run
// of unbroken parts past meetMaxParts also closes it — otherwise a continuous
// stream is transcribed for as long as it plays — and additionally holds capture
// until genuine silence arrives, so the same audio is not picked straight back up.
func segmentClose(ended bool, part uint64) (final, hold bool) {
	if ended {
		return true, false
	}
	capped := part+1 >= meetMaxParts
	return capped, capped
}

// listenMeetAudio captures PipeWire output from meeting browsers and Teams.
// It never falls back to the default system monitor, excluding unrelated audio.
func liveModeOf(stream bool) string {
	if stream {
		return liveModeStream
	}
	return liveModeBatch
}

func (a *App) listenMeetAudio(ctx context.Context) {
	target, err := a.audioTarget(ctx)
	if err != nil {
		// Same key as the success line below, so the log shows each change of
		// state once instead of every two-second retry.
		logDedup("audio-target", "no audio target: %v", err)
		a.setListenStatus("Fuente de audio: " + err.Error())
		if waitForRetry(ctx) {
			go a.listenMeetAudio(ctx)
		}
		return
	}
	captureCtx, stopCapture := context.WithCancel(ctx)
	defer stopCapture()
	command := exec.CommandContext(captureCtx, "pw-record", "--target", target, "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	stdout, err := command.StdoutPipe()
	if err == nil {
		err = command.Start()
	}
	if err != nil {
		logf("pw-record could not attach to target %s: %v", target, err)
		a.setListeningFailure("No se pudo conectar al audio de la reunión")
		return
	}
	defer command.Wait()
	// Which node was picked is the first thing to check when the transcript is
	// empty or carries the wrong audio.
	logDedup("audio-target", "capturing PipeWire target %s", target)
	a.setListenStatus("Escuchando audio de la reunión")

	// Meeting apps may open a new PipeWire node when a call starts playing, so the node
	// picked at connect time can stay silent forever. Watch for a better one and
	// drop the current recorder so the reconnect path retargets.
	var speaking atomic.Bool
	go watchChromeStream(captureCtx, target, &speaking, stopCapture, a.audioTarget)

	frame := make([]byte, audio.FrameBytes)
	segment := make([]byte, 0, audio.SampleRate*2*12)
	ambient := &audio.NoiseFloor{}
	speech := audio.NewSpeechDetector()
	recording, startFrames, voicedFrames, silenceFrames := false, 0, 0, 0
	// Set when a run of unbroken audio was cut off: nothing new is recorded
	// until a genuinely quiet frame proves the stream actually paused.
	awaitingSilence := false
	var turn meetSegment
	// liveRT is the streaming session of the turn being recorded (live mode only).
	var liveRT *liveRuntime
	liveOn := a.liveStreamingEnabled()
	// abortLive gives up the current live turn after a streaming error: the
	// failure is shown and nothing is recorded until real silence returns —
	// there is no fallback to batch transcription.
	abortLive := func(err error) {
		logDedup("live-stream", "live transcription: %v", err)
		a.setListenStatus("Transcripción en vivo: " + err.Error())
		liveRT, recording, awaitingSilence = nil, false, true
		startFrames, voicedFrames, silenceFrames = 0, 0, 0
		speaking.Store(false)
		segment = segment[:0]
	}
	for {
		if _, err := io.ReadFull(stdout, frame); err != nil {
			// Pausing cancels the context and kills the recorder, so this is
			// also where a segment cut short by the user gets sent.
			if recording && liveRT != nil {
				liveRT.end(time.Now(), voicedFrames < meetVoiceFrames)
			} else if recording && voicedFrames >= meetVoiceFrames {
				turn.pcm, turn.final = segment, true
				turn.part++
				a.queueMeetSegment(turn)
			}
			a.setAudioLevel(0)
			if ctx.Err() == nil {
				logDedup("recorder", "recorder on target %s stopped (%v); reconnecting", target, err)
				a.setListenStatus("Cambió el audio de la reunión; reconectando…")
				if waitForRetry(ctx) {
					go a.listenMeetAudio(ctx)
				}
			}
			return
		}
		level := audio.RMS(frame)
		a.setAudioLevel(level)
		loud := level >= ambient.Threshold()
		if !loud {
			ambient.Observe(level)
		}
		voice := audio.IsVoice(speech, frame, loud)
		if voice {
			a.lastOutputVoice.Store(time.Now().UnixMilli())
		}
		if !recording {
			if awaitingSilence {
				// Only a quiet frame clears the hold; while the continuous audio
				// keeps playing nothing is recorded or billed.
				if !voice {
					awaitingSilence = false
					a.setListenStatus("Escuchando audio de la reunión")
				}
				startFrames, segment = 0, segment[:0]
				continue
			}
			if voice {
				startFrames++
				segment = append(segment, frame...)
				if startFrames >= meetStartFrames {
					recording, silenceFrames, voicedFrames = true, 0, startFrames
					turn = a.nextMeetTurn()
					speaking.Store(true)
					a.setListenStatus("Intervención detectada…")
					if liveOn {
						rt, err := a.ensureLive(ctx)
						if err == nil {
							err = rt.begin(turn, segment)
						}
						if err != nil {
							abortLive(err)
							continue
						}
						liveRT = rt
					}
				}
			} else {
				startFrames, segment = 0, segment[:0]
			}
			continue
		}
		segment = append(segment, frame...)
		if voice {
			silenceFrames = 0
			voicedFrames++
		} else {
			silenceFrames++
		}
		ended := silenceFrames >= meetSilenceFrames
		if liveRT != nil {
			if err := liveRT.frame(frame); err != nil {
				abortLive(err)
				continue
			}
			switch {
			case voice:
				liveRT.cancelSpeculation()
			case silenceFrames == meetSpecFrames:
				liveRT.speculate()
			}
			long := len(segment)/audio.FrameBytes >= liveMaxFrames
			if !ended && !long {
				continue
			}
			voiceEnd := time.Now().Add(-time.Duration(silenceFrames) * 100 * time.Millisecond)
			if err := liveRT.end(voiceEnd, voicedFrames < meetVoiceFrames); err != nil {
				abortLive(err)
				continue
			}
			if !ended {
				logf("live turn %d/%d stopped after %ds without a pause; waiting for silence", turn.session, turn.turn, liveMaxFrames/10)
				a.setListenStatus("Audio continuo: esperando una pausa real")
				awaitingSilence = true
			} else {
				a.setListenStatus("Escuchando audio de la reunión")
			}
			liveRT, recording, startFrames, voicedFrames, silenceFrames = nil, false, 0, 0, 0
			speaking.Store(false)
			segment = segment[:0]
			continue
		}
		if !ended && len(segment)/audio.FrameBytes < meetMaximumFrames {
			continue
		}
		final, capped := segmentClose(ended, turn.part)
		// A burst that never carried enough voice was background noise, not an
		// intervention: sending it only buys an empty transcription.
		if (voicedFrames >= meetVoiceFrames && len(segment)/audio.FrameBytes >= meetMinimumFrames) || (ended && turn.part > 0) {
			turn.part++
			turn.final = final
			if ended {
				turn.pcm = audio.TrimTrailingSilence(segment, silenceFrames, meetKeepSilence, meetMinimumFrames)
			} else {
				turn.pcm = segment
			}
			a.queueMeetSegment(turn)
		}
		if ctx.Err() != nil {
			return
		}
		if !ended && !capped {
			// The 15s ceiling hit mid-sentence. Keep recording and replay a short
			// tail, so a long question stays one continuous capture instead of
			// losing whatever is said while the first half is in flight.
			segment = append(segment[:0], audio.TailFrames(segment, meetOverlapFrames)...)
			silenceFrames, voicedFrames = 0, meetVoiceFrames
			a.setListenStatus("Intervención larga; sigo escuchando…")
			continue
		}
		if !ended {
			// Past this many unbroken parts it is not an intervention: nobody
			// talks for minutes without a half-second pause. It is music or a
			// shared video, and continuing would transcribe wall-clock time for
			// as long as it plays. Wait for real silence before listening again.
			logf("turn %d/%d stopped after %d unbroken parts; waiting for silence",
				turn.session, turn.turn, turn.part)
			a.setListenStatus("Audio continuo: esperando una pausa real")
			awaitingSilence = true
		}
		recording, startFrames, voicedFrames, silenceFrames = false, 0, 0, 0
		speaking.Store(false)
		segment = segment[:0]
		a.setListenStatus("Escuchando audio de la reunión")
	}
}

// watchChromeStream drops the current recorder when Chrome exposes a different
// output node, so a call that starts after the app connected is still captured.
func watchChromeStream(ctx context.Context, target string, speaking *atomic.Bool, stopCapture context.CancelFunc, choose func(context.Context) (string, error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		// Never cut a segment that is mid-sentence.
		if speaking.Load() {
			continue
		}
		current, err := choose(ctx)
		if err != nil {
			stopCapture()
			return
		}
		if current == target {
			continue
		}
		stopCapture()
		return
	}
}

func (a *App) setListenStatus(status string) { a.mu.Lock(); a.listenStatus = status; a.mu.Unlock() }

func (a *App) setListeningFailure(status string) {
	a.mu.Lock()
	a.listening = false
	a.listenerCancel = nil
	a.listenStatus = status
	a.transcript = status
	a.transcriptFinal = false
	a.mu.Unlock()
}

func waitForRetry(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(2 * time.Second):
		return true
	}
}
