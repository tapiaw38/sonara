package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Phase 2: speaker diarization of the meeting_output track. It is optional,
// additive, and off by default (SONARA_DIARIZE_ENABLED=1 to turn it on): a
// meeting transcribes exactly as in phase 1 whether or not this is enabled or
// available, and nothing here ever blocks recording or transcription. If the
// local service cannot be reached, participants simply stay "Participante"
// instead of "Participante 1/2/…".
//
// Unlike transcription, diarization runs once, after capture stops, on the
// whole accumulated meeting_output track rather than per block: pyannote
// assigns its speaker labels independently on every call, so diarizing one
// short block at a time would give each block its own, mutually
// incompatible "SPEAKER_00" — stable identity across blocks needs the model to
// see them together. That means this track's audio — never the microphone's —
// is held in memory for the length of the meeting instead of being discarded
// per block, and only when this feature is on; see diarizeMaxDuration for the
// memory this can use and its cap.
//
// This also means the microphone is never diarized or voice-matched: "Yo" is
// assigned by which device captured it, not by analyzing the audio. Matching a
// user's own voice inside the meeting_output mix (phase 3, to catch an echo of
// themselves through someone else's speakers) is not implemented.

// diarizeHealthTimeout and diarizeHealthPoll are vars, not consts, so tests
// can shrink them instead of waiting on the real (minutes-long) model load.
var (
	// diarizeHealthTimeout covers the model loading on first use.
	diarizeHealthTimeout = 90 * time.Second
	diarizeHealthPoll    = 1 * time.Second
)

const (
	// diarizeFinalTimeout bounds one whole-meeting diarization call. pyannote's
	// own published figure (31s per hour of audio) is for an H100; a laptop CPU
	// has no published number, so this is generous rather than measured.
	diarizeFinalTimeout = 15 * time.Minute
	// diarizeMaxDuration caps the accumulated meeting_output audio: past this,
	// further audio is dropped from the diarization pass (the meeting itself
	// keeps transcribing normally; only the extra minutes stay unlabeled).
	diarizeMaxDuration = 3 * time.Hour
	diarizeMaxBytes    = int64(diarizeMaxDuration/time.Second) * meetSampleRate * 2
)

func diarizeEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SONARA_DIARIZE_ENABLED")))
	return v == "1" || v == "true"
}

func diarizeURL() string {
	if v := strings.TrimSpace(os.Getenv("SONARA_DIARIZE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8778"
}

func diarizePython() string {
	if v := strings.TrimSpace(os.Getenv("SONARA_DIARIZE_PYTHON")); v != "" {
		return v
	}
	return "python3"
}

func diarizeScript() string {
	if v := strings.TrimSpace(os.Getenv("SONARA_DIARIZE_SCRIPT")); v != "" {
		return v
	}
	return filepath.Join("diarize", "server.py")
}

type diarizedSpan struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Speaker string `json:"speaker"`
}

type diarizeResponse struct {
	Speakers []diarizedSpan `json:"speakers"`
	Error    string         `json:"error,omitempty"`
}

// diarizer manages the local Python service: one per App, started lazily on
// first use and kept running for as long as the app is open. A failure to
// start or to become healthy is permanent for this diarizer (no retry storm
// against a broken local setup); fixing it needs an app restart.
type diarizer struct {
	baseURL string
	client  *http.Client

	mu      sync.Mutex
	cmd     *exec.Cmd
	ready   bool
	failed  bool
	failure error

	// startProcess launches the local service; replaced in tests so no real
	// Python subprocess is spawned.
	startProcess func() (*exec.Cmd, error)
}

func newDiarizer() *diarizer {
	return &diarizer{
		baseURL: diarizeURL(),
		client:  &http.Client{Timeout: diarizeFinalTimeout},
		startProcess: func() (*exec.Cmd, error) {
			cmd := exec.Command(diarizePython(), diarizeScript())
			cmd.Stderr = diarizeLogWriter{}
			cmd.Stdout = diarizeLogWriter{}
			return cmd, cmd.Start()
		},
	}
}

// diarizeLogWriter prefixes the service's own stderr/stdout into Sonara's log
// without ever logging the audio or transcript content that service handles.
type diarizeLogWriter struct{}

func (diarizeLogWriter) Write(p []byte) (int, error) {
	logf("diarize service: %s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// ensure starts the service if needed and waits for it to answer /health. It
// is cheap to call repeatedly: once ready (or failed), it returns immediately.
func (d *diarizer) ensure(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ready {
		return nil
	}
	if d.failed {
		return d.failure
	}
	if d.cmd == nil {
		cmd, err := d.startProcess()
		if err != nil {
			d.failed, d.failure = true, fmt.Errorf("no se pudo iniciar el servicio de diarización: %w", err)
			return d.failure
		}
		d.cmd = cmd
	}
	deadline := time.Now().Add(diarizeHealthTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/health", nil)
		if err == nil {
			if resp, err := d.client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					d.ready = true
					return nil
				}
			}
		}
		if !time.Now().Before(deadline) {
			d.failed, d.failure = true, fmt.Errorf("el servicio de diarización no respondió en %s", diarizeHealthTimeout)
			return d.failure
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(diarizeHealthPoll):
		}
	}
}

// diarize sends one WAV file (the whole meeting_output track) and returns its
// speaker spans, each independently labeled by the service — stabilizing
// those labels across a session is stableSpeakerKeys's job, not this call's.
func (d *diarizer) diarize(ctx context.Context, wav []byte) ([]diarizedSpan, error) {
	if err := d.ensure(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/diarize", bytes.NewReader(wav))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "audio/wav")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var out diarizeResponse
	if resp.StatusCode != http.StatusOK {
		message := fmt.Sprintf("estado %d", resp.StatusCode)
		if json.Unmarshal(body, &out) == nil && out.Error != "" {
			message = out.Error
		}
		return nil, fmt.Errorf("el servicio de diarización falló: %s", message)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("el servicio de diarización devolvió una respuesta inválida")
	}
	return out.Speakers, nil
}

// stop ends the subprocess, if one was started. Safe to call even when
// diarization was never used.
func (d *diarizer) stop() {
	d.mu.Lock()
	cmd := d.cmd
	d.cmd = nil
	d.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// --- Accumulating the meeting_output track for the final diarization pass ---

// appendDiarizePCM buffers one frame of the meeting_output track, origin-
// aligned to the recording's shared clock. No-op once diarizeMaxBytes is
// reached: the rest of the meeting keeps transcribing, just without diarized
// names past that point.
func (r *meetingRun) appendDiarizePCM(frame []byte) {
	r.diarizeMu.Lock()
	defer r.diarizeMu.Unlock()
	room := diarizeMaxBytes - int64(len(r.diarizePCM))
	if room < int64(len(frame)) {
		if room < 0 {
			room = 0
		}
		frame = frame[:room]
		if !r.diarizeCapped {
			r.diarizeCapped = true
			logDedup("diarize-cap", "meeting output exceeded %s; later audio will not be diarized", diarizeMaxDuration)
		}
	}
	r.diarizePCM = append(r.diarizePCM, frame...)
}

// padDiarizeSilence keeps the accumulator aligned to the shared clock across a
// gap in capture: without this, a reconnect would shift every later speaker
// span out of sync with the segments it is matched against.
func (r *meetingRun) padDiarizeSilence(durationMS int64) {
	if durationMS <= 0 {
		return
	}
	silence := make([]byte, durationMS*meetSampleRate*2/1000)
	r.appendDiarizePCM(silence)
}

// overlapSpeaker returns the raw speaker label covering the most time inside
// [startMS, endMS) — a segment is attributed to whoever spoke the most during
// it, the same block-level granularity transcription already uses.
func overlapSpeaker(spans []diarizedSpan, startMS, endMS int64) string {
	totals := map[string]int64{}
	best, bestMS := "", int64(0)
	for _, span := range spans {
		from, to := max(span.StartMS, startMS), min(span.EndMS, endMS)
		if to <= from {
			continue
		}
		totals[span.Speaker] += to - from
		if totals[span.Speaker] > bestMS {
			best, bestMS = span.Speaker, totals[span.Speaker]
		}
	}
	return best
}

// stableSpeakerKeys renumbers one diarization call's raw labels ("SPEAKER_00",
// whatever the model used) into "spk_0", "spk_1"… ordered by first appearance,
// so storage and display never depend on the model's own naming.
func stableSpeakerKeys(spans []diarizedSpan) map[string]string {
	sorted := append([]diarizedSpan(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].StartMS < sorted[j].StartMS })
	keys := map[string]string{}
	for _, span := range sorted {
		if _, seen := keys[span.Speaker]; !seen {
			keys[span.Speaker] = fmt.Sprintf("spk_%d", len(keys))
		}
	}
	return keys
}

// runDiarization is the final pass: best-effort, and the only place that reads
// run.diarizePCM. It never fails the meeting — every error is logged and
// swallowed, leaving segments with their generic track label.
func (a *App) runDiarization(run *meetingRun) {
	if !diarizeEnabled() {
		return
	}
	run.diarizeMu.Lock()
	pcm := run.diarizePCM
	run.diarizePCM = nil // release it whether or not diarization succeeds
	run.diarizeMu.Unlock()
	if len(pcm) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), diarizeFinalTimeout)
	defer cancel()
	spans, err := a.diarizer.diarize(ctx, wavFromPCM(pcm))
	if err != nil {
		logf("meeting %s: diarization skipped: %v", run.sessionID, err)
		return
	}
	if len(spans) == 0 {
		return
	}
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return
	}
	segments, err := database.transcriptSegments(run.sessionID)
	if err != nil {
		logf("meeting %s: could not reload segments for diarization: %v", run.sessionID, err)
		return
	}
	keys := stableSpeakerKeys(spans)
	for _, segment := range segments {
		if segment.Track != trackOutput || segment.Status != segmentOK {
			continue
		}
		raw := overlapSpeaker(spans, segment.StartMS, segment.EndMS)
		if raw == "" {
			continue
		}
		key := keys[raw]
		if err := database.setSegmentSpeaker(segment.ID, key); err != nil {
			logf("meeting %s: could not store the speaker for segment %d: %v", run.sessionID, segment.ID, err)
			continue
		}
		segment.SpeakerKey = key
		a.emit("meeting:segment", MeetingSegmentEvent{SessionID: run.sessionID, Segment: segment})
	}
	logf("meeting %s: diarization found %d participant(s)", run.sessionID, len(keys))
}
