package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phase 2: speaker diarization of the meeting_output track, via the Gemini
// API (cloud) rather than a local model — no PyTorch, no local service, no
// Hugging Face token to install on the user's machine. It is optional,
// additive, and off unless a Gemini key is configured (Settings): a meeting
// transcribes exactly as in phase 1 whether or not this is set up, and
// nothing here ever blocks recording or transcription. If the call fails,
// participants simply stay "Participante" instead of "Participante 1/2/…".
//
// Unlike transcription, diarization runs once, after capture stops, on the
// whole accumulated meeting_output track rather than per block: a fresh
// Gemini call has no memory of a previous one, so diarizing short blocks
// independently would give each one its own, mutually incompatible "Speaker
// 1" — stable identity across the meeting needs the model to see it all at
// once. That means this track's audio — never the microphone's — is held in
// memory for the length of the meeting instead of being discarded per block,
// and only when a Gemini key is configured; see diarizeMaxDuration for the
// memory this can use and its cap.
//
// This also means the microphone is never diarized or voice-matched: "Yo" is
// assigned by which device captured it, not by analyzing the audio. Matching a
// user's own voice inside the meeting_output mix, to catch an echo of
// themselves through someone else's speakers, is phase 3 — see voicematch.go.
//
// *** UNVERIFIED AGAINST A REAL KEY ***: this talks to
// generativelanguage.googleapis.com's Files and Interactions APIs per their
// public docs (ai.google.dev, fetched 2026-11-16), but has never been run
// against a real Gemini account — see PLAN_TRANSCRIPCION_REUNIONES.md.

// geminiBaseURL and geminiUploadURL are vars, not consts, so tests can point
// them at a fake server instead of the real Google host.
var (
	geminiBaseURL   = "https://generativelanguage.googleapis.com/v1beta"
	geminiUploadURL = "https://generativelanguage.googleapis.com/upload/v1beta/files"
)

const (
	geminiAPIRevision = "2026-05-20"
	// diarizeFinalTimeout bounds the whole final pass: one file upload plus
	// one Gemini call, for up to diarizeMaxDuration of audio.
	diarizeFinalTimeout = 15 * time.Minute
	// diarizeMaxDuration caps the accumulated meeting_output audio: past this,
	// further audio is dropped from the diarization pass (the meeting itself
	// keeps transcribing normally; only the extra minutes stay unlabeled).
	// Well under the Files API's stated 2GB per-file cap; chosen as a memory
	// bound for this app, not a Gemini limit.
	diarizeMaxDuration = 3 * time.Hour
	diarizeMaxBytes    = int64(diarizeMaxDuration/time.Second) * meetSampleRate * 2
)

func geminiModelName() string {
	if v := strings.TrimSpace(os.Getenv("GEMINI_MODEL")); v != "" {
		return v
	}
	return "gemini-4-hydrogen"
}

// diarizePrompt asks for speaker turns only, not a transcript: Sonara already
// has the words from its own ASR pipeline and only needs who said them.
const diarizePrompt = "This is the audio captured from one side of a meeting (everyone except the listener). " +
	"Identify who is speaking and when. Return ONLY a JSON array of speaker turns: each item has " +
	"start_ms and end_ms (integer milliseconds from the very start of this audio) and speaker " +
	"(a short label such as \"Speaker 1\", kept the same every time that voice speaks again). " +
	"Merge consecutive turns from the same speaker. Do not transcribe any words. Ignore silence."

var diarizeSchema = map[string]any{
	"type": "array",
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"start_ms": map[string]any{"type": "integer"},
			"end_ms":   map[string]any{"type": "integer"},
			"speaker":  map[string]any{"type": "string"},
		},
		"required": []string{"start_ms", "end_ms", "speaker"},
	},
}

type diarizedSpan struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Speaker string `json:"speaker"`
}

// diarizer is one per App: a thin HTTP client plus the Gemini API key, set
// from Settings (SetGeminiAPIKey) the same way the DeepSeek key is.
type diarizer struct {
	client *http.Client
	keyMu  sync.RWMutex
	apiKey string
}

func newDiarizer() *diarizer {
	return &diarizer{client: &http.Client{Timeout: diarizeFinalTimeout}}
}

func (d *diarizer) SetAPIKey(key string) {
	d.keyMu.Lock()
	d.apiKey = strings.TrimSpace(key)
	d.keyMu.Unlock()
}

func (d *diarizer) key() string {
	d.keyMu.RLock()
	defer d.keyMu.RUnlock()
	return d.apiKey
}

// Configured is also how the rest of the app decides diarization is "on":
// setting a key in Settings is the opt-in, with no separate feature flag.
func (d *diarizer) period() time.Duration {
	if d.key() == "" {
		return localDiarizePeriod
	}
	return diarizePeriod
}

func (d *diarizer) minGrowth() int {
	if d.key() == "" {
		return localMinGrowth
	}
	return diarizeMinGrowth
}

func (d *diarizer) Configured() bool { return d.key() != "" || localDiarizerBin() != "" }

// diarize sends one WAV file (the whole meeting_output track) and returns its
// speaker spans, each independently labeled by the model — stabilizing those
// labels across a session is stableSpeakerKeys's job, not this call's.
// speakers fixes how many people to find (local engine only); 0 guesses.
func (d *diarizer) diarize(ctx context.Context, wav []byte, speakers int) ([]diarizedSpan, error) {
	apiKey := d.key()
	if apiKey == "" {
		return localDiarize(ctx, wav, speakers)
	}
	fileURI, fileName, mimeType, err := d.uploadFile(ctx, apiKey, wav)
	if err != nil {
		return nil, fmt.Errorf("no se pudo subir el audio a Gemini: %w", err)
	}
	// Best-effort cleanup on an independent context: a canceled caller must
	// not skip deleting what was already uploaded. Google expires it by
	// itself after 48h either way (see PLAN_TRANSCRIPCION_REUNIONES.md).
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.deleteFile(cleanupCtx, apiKey, fileName); err != nil {
			logf("could not delete the meeting audio uploaded to Gemini: %v", err)
		}
	}()
	return d.requestDiarization(ctx, apiKey, fileURI, mimeType)
}

// uploadFile runs the Files API's resumable upload in two requests: one to
// start the session and get an upload URL, one to send the bytes.
func (d *diarizer) uploadFile(ctx context.Context, apiKey string, wav []byte) (uri, name, mimeType string, err error) {
	mimeType = "audio/wav"
	start, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiUploadURL,
		strings.NewReader(`{"file":{"display_name":"sonara-meeting"}}`))
	if err != nil {
		return "", "", "", err
	}
	start.Header.Set("x-goog-api-key", apiKey)
	start.Header.Set("Content-Type", "application/json")
	start.Header.Set("X-Goog-Upload-Protocol", "resumable")
	start.Header.Set("X-Goog-Upload-Command", "start")
	start.Header.Set("X-Goog-Upload-Header-Content-Length", strconv.Itoa(len(wav)))
	start.Header.Set("X-Goog-Upload-Header-Content-Type", mimeType)
	startResp, err := d.client.Do(start)
	if err != nil {
		return "", "", "", err
	}
	uploadURL := startResp.Header.Get("x-goog-upload-url")
	io.Copy(io.Discard, io.LimitReader(startResp.Body, 1<<16))
	startResp.Body.Close()
	if startResp.StatusCode < 200 || startResp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("estado %d al iniciar la subida", startResp.StatusCode)
	}
	if uploadURL == "" {
		return "", "", "", fmt.Errorf("la respuesta no incluyó la URL de subida")
	}

	put, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(wav))
	if err != nil {
		return "", "", "", err
	}
	put.Header.Set("Content-Length", strconv.Itoa(len(wav)))
	put.Header.Set("X-Goog-Upload-Offset", "0")
	put.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	putResp, err := d.client.Do(put)
	if err != nil {
		return "", "", "", err
	}
	defer putResp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(putResp.Body, 1<<16))
	if err != nil {
		return "", "", "", err
	}
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("estado %d al subir el audio", putResp.StatusCode)
	}
	var decoded struct {
		File struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"file"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.File.URI == "" {
		return "", "", "", fmt.Errorf("la subida no devolvió un archivo usable")
	}
	return decoded.File.URI, decoded.File.Name, mimeType, nil
}

// deleteFile is best-effort: its exact endpoint was not confirmed against
// Google's own docs in this session (only that deletion is possible through
// the API). A failure here never affects the meeting; see the doc comment.
func (d *diarizer) deleteFile(ctx context.Context, apiKey, fileName string) error {
	if fileName == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, geminiBaseURL+"/"+fileName, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("estado %d", resp.StatusCode)
	}
	return nil
}

type geminiInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// URI is for Files-API-uploaded audio; Data is inline base64 bytes (small
	// clips, like a voice sample, can skip the upload round trip entirely).
	URI      string `json:"uri,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type geminiResponseFormat struct {
	Type     string         `json:"type"`
	MimeType string         `json:"mime_type"`
	Schema   map[string]any `json:"schema"`
}

type geminiRequest struct {
	Model          string               `json:"model"`
	Input          []geminiInput        `json:"input"`
	ResponseFormat geminiResponseFormat `json:"response_format"`
}

type geminiInteraction struct {
	Status string `json:"status"`
	Steps  []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"steps"`
}

func (d *diarizer) requestDiarization(ctx context.Context, apiKey, fileURI, mimeType string) ([]diarizedSpan, error) {
	text, err := d.interact(ctx, apiKey, geminiRequest{
		Model: geminiModelName(),
		Input: []geminiInput{
			{Type: "text", Text: diarizePrompt},
			{Type: "audio", URI: fileURI, MimeType: mimeType},
		},
		ResponseFormat: geminiResponseFormat{Type: "text", MimeType: "application/json", Schema: diarizeSchema},
	})
	if err != nil {
		return nil, err
	}
	var spans []diarizedSpan
	if err := json.Unmarshal([]byte(text), &spans); err != nil {
		return nil, fmt.Errorf("Gemini no devolvió la lista de hablantes esperada")
	}
	return spans, nil
}

// interact is the shared POST /interactions call: build the request, check
// the HTTP and interaction status, and concatenate the model's text output.
// Callers each decode that text into their own expected JSON shape.
func (d *diarizer) interact(ctx context.Context, apiKey string, request geminiRequest) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiBaseURL+"/interactions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Revision", geminiAPIRevision)
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Gemini respondió con estado %d", resp.StatusCode)
	}
	var decoded geminiInteraction
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return "", fmt.Errorf("Gemini devolvió una respuesta inválida")
	}
	if decoded.Status != "completed" {
		return "", fmt.Errorf("Gemini no completó la solicitud (estado %q)", decoded.Status)
	}
	var text strings.Builder
	for _, step := range decoded.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, part := range step.Content {
			if part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("Gemini no devolvió resultado")
	}
	return text.String(), nil
}

// --- Accumulating the meeting_output track for the final diarization pass ---

// appendDiarizePCM buffers one frame of the meeting_output track, origin-
// aligned to the recording's shared clock. No-op once diarizeMaxBytes is
// reached: the rest of the meeting keeps transcribing, just without diarized
// names past that point.
func (r *meetingRun) diarizeBuffer(track string) *[]byte {
	if track == trackMicrophone {
		return &r.diarizeMicPCM
	}
	return &r.diarizePCM
}

// diarizeSnapshot is a stable prefix of a track's buffer: append only ever
// writes past len.
func (r *meetingRun) diarizeSnapshot(track string) []byte {
	r.diarizeMu.Lock()
	defer r.diarizeMu.Unlock()
	return *r.diarizeBuffer(track)
}

func (r *meetingRun) appendDiarizePCM(track string, frame []byte) {
	r.diarizeMu.Lock()
	defer r.diarizeMu.Unlock()
	buffer := r.diarizeBuffer(track)
	room := diarizeMaxBytes - int64(len(*buffer))
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
	*buffer = append(*buffer, frame...)
}

// releaseDiarizePCM frees the accumulated meeting_output audio. Called once
// from finishMeeting after both runDiarization and runVoiceMatch have had
// their chance to read it — never from inside either pass itself.
func (r *meetingRun) releaseDiarizePCM() {
	r.diarizeMu.Lock()
	r.diarizePCM, r.diarizeMicPCM = nil, nil
	r.diarizeMu.Unlock()
}

// padDiarizeSilence keeps the accumulator aligned to the shared clock across a
// gap in capture: without this, a reconnect would shift every later speaker
// span out of sync with the segments it is matched against.
func (r *meetingRun) padDiarizeSilence(track string, durationMS int64) {
	if durationMS <= 0 {
		return
	}
	silence := make([]byte, durationMS*meetSampleRate*2/1000)
	r.appendDiarizePCM(track, silence)
}

// overlapMS is how much of [startMS, endMS) a span covers — zero when they
// don't overlap at all.
func overlapMS(spanStart, spanEnd, startMS, endMS int64) int64 {
	from, to := max(spanStart, startMS), min(spanEnd, endMS)
	if to <= from {
		return 0
	}
	return to - from
}

// overlapSpeaker returns the raw speaker label covering the most time inside
// [startMS, endMS) — a segment is attributed to whoever spoke the most during
// it, the same block-level granularity transcription already uses.
func overlapSpeaker(spans []diarizedSpan, startMS, endMS int64) string {
	totals := map[string]int64{}
	best, bestMS := "", int64(0)
	for _, span := range spans {
		totals[span.Speaker] += overlapMS(span.StartMS, span.EndMS, startMS, endMS)
		if totals[span.Speaker] > bestMS {
			best, bestMS = span.Speaker, totals[span.Speaker]
		}
	}
	return best
}

// stableSpeakerKeys renumbers one diarization call's raw labels ("Speaker 1",
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
func (a *App) setDiarizeNote(sessionID, note string) {
	a.mu.Lock()
	a.diarizeNote, a.diarizeNoteSession = note, sessionID
	a.mu.Unlock()
}

func (a *App) runDiarization(run *meetingRun) {
	if !a.diarizer.Configured() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), diarizeFinalTimeout)
	defer cancel()
	found, failed := 0, false
	for _, track := range []string{trackMicrophone, trackOutput} {
		// Only a snapshot: runVoiceMatch (phase 3) may still need the same buffer
		// right after this. finishMeeting releases it once, after both have run.
		if pcm := run.diarizeSnapshot(track); len(pcm) > 0 {
			count := a.applyDiarization(ctx, run, track, pcm)
			failed = failed || count < 0
			found += max(count, 0)
		}
	}
	if found == 0 && failed {
		return // applyDiarization already left the failure note
	}
	if found == 0 {
		a.setDiarizeNote(run.sessionID, "No se detectaron participantes para separar.")
		return
	}
	a.setDiarizeNote(run.sessionID, fmt.Sprintf("Participantes detectados: %d.", found))
	a.nameParticipants(ctx, run.sessionID)
}

// diarizePeriod is how often the participants are re-separated while the
// meeting is still being recorded. A var so tests do not wait minutes.
var diarizePeriod = 3 * time.Minute

// The local engine costs nothing per pass, so it re-separates far more often:
// letters show up within seconds instead of minutes. Each pass still reads the
// whole recording (about 0.1s of CPU per second of audio), so a very long
// meeting makes each pass slower.
var (
	localDiarizePeriod = 20 * time.Second
	localMinGrowth     = 10 * meetSampleRate * 2
)

const (
	// diarizeMinGrowth is how much new audio (60 s) a pass needs to be worth
	// re-uploading the whole recording again.
	diarizeMinGrowth = 60 * meetSampleRate * 2
)

// periodicDiarization re-separates the participants while the meeting runs, so
// names appear during the call and not only after it. Each pass uploads the
// whole recording so far (a fresh Gemini call has no memory of the last one),
// which is why it only runs when a minute of new audio has accumulated, and
// why the cost of a long meeting grows with every pass.
//
// Every pass relabels every segment it covers, and numbering follows who
// speaks first in the audio: it is stable while the early speakers stay the
// same, but a later pass can reassign a number, and a name typed for a number
// (RenameMeetingSpeaker) follows the number, not the person. The final pass
// after the meeting always has the last word.
func (a *App) periodicDiarization(ctx context.Context, run *meetingRun) {
	defer run.periodic.Done()
	lastBytes := map[string]int{}
	var lastNamed time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.diarizer.period()):
		}
		if !a.diarizer.Configured() {
			continue
		}
		found := 0
		for _, track := range []string{trackMicrophone, trackOutput} {
			pcm := run.diarizeSnapshot(track)
			if len(pcm)-lastBytes[track] < a.diarizer.minGrowth() {
				continue
			}
			lastBytes[track] = len(pcm)
			passCtx, cancel := context.WithTimeout(ctx, diarizeFinalTimeout)
			found += max(a.applyDiarization(passCtx, run, track, pcm), 0)
			cancel()
		}
		if found > 0 {
			a.setDiarizeNote(run.sessionID, fmt.Sprintf("Participantes detectados hasta ahora: %d (se actualiza durante la reunión).", found))
			// Names mentioned so far show up during the meeting, not only after
			// it; at most once a minute, since each pass reads the whole text.
			if time.Since(lastNamed) >= time.Minute {
				lastNamed = time.Now()
				nameCtx, cancel := context.WithTimeout(ctx, time.Minute)
				a.nameParticipants(nameCtx, run.sessionID)
				cancel()
			}
		}
	}
}

// applyDiarization separates the speakers in one track's pcm and labels that
// track's stored segments. It returns how many participants it found, or -1
// when the separation itself failed.
func (a *App) applyDiarization(ctx context.Context, run *meetingRun, track string, pcm []byte) int {
	speakers := 0
	if track == trackMicrophone {
		speakers = a.MeetingPeople()
	}
	spans, err := a.diarizer.diarize(ctx, wavFromPCM(pcm), speakers)
	if err != nil {
		if ctx.Err() == nil {
			logf("meeting %s: diarization of %s skipped: %v", run.sessionID, track, err)
			a.setDiarizeNote(run.sessionID, "No se pudieron separar los participantes: "+err.Error())
		}
		return -1
	}
	if len(spans) == 0 {
		return 0
	}
	spans = mergeMinorSpeakers(spans)
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return 0
	}
	segments, err := database.transcriptSegments(run.sessionID)
	if err != nil {
		logf("meeting %s: could not reload segments for diarization: %v", run.sessionID, err)
		return 0
	}
	keys := stableSpeakerKeys(spans)
	for _, segment := range segments {
		if segment.Track != track || segment.Status != segmentOK {
			continue
		}
		raw := overlapSpeaker(spans, segment.StartMS, segment.EndMS)
		if raw == "" {
			continue
		}
		key := keys[raw]
		if segment.SpeakerKey == key {
			continue // nothing changed: do not re-announce it
		}
		if err := database.setSegmentSpeaker(segment.ID, key); err != nil {
			logf("meeting %s: could not store the speaker for segment %d: %v", run.sessionID, segment.ID, err)
			continue
		}
		segment.SpeakerKey = key
		a.emit("meeting:segment", MeetingSegmentEvent{SessionID: run.sessionID, Segment: segment})
	}
	logf("meeting %s: diarization of %s found %d participant(s)", run.sessionID, track, len(keys))
	return len(keys)
}

// Diarization over-splits: one voice at a new distance or pitch becomes a new
// "speaker". Like the minimum-speaker-duration step in Microsoft's VoxCeleb
// 2020 system, a label with too little total speech is not a person: its spans
// go to whichever real speaker talked nearest in time.
const (
	minSpeakerMS    = 3000
	minSpeakerShare = 0.05
)

func mergeMinorSpeakers(spans []diarizedSpan) []diarizedSpan {
	totals := map[string]int64{}
	var all int64
	for _, span := range spans {
		totals[span.Speaker] += span.EndMS - span.StartMS
		all += span.EndMS - span.StartMS
	}
	limit := max(int64(minSpeakerMS), int64(float64(all)*minSpeakerShare))
	major := func(speaker string) bool { return totals[speaker] >= limit }
	hasMajor := false
	for speaker := range totals {
		hasMajor = hasMajor || major(speaker)
	}
	if !hasMajor {
		return spans // nobody spoke long enough to judge: keep what was found
	}
	merged := make([]diarizedSpan, 0, len(spans))
	for _, span := range spans {
		if !major(span.Speaker) {
			best, bestGap := "", int64(-1)
			for _, other := range spans {
				if !major(other.Speaker) {
					continue
				}
				gap := max(other.StartMS-span.EndMS, span.StartMS-other.EndMS, 0)
				if bestGap < 0 || gap < bestGap {
					best, bestGap = other.Speaker, gap
				}
			}
			span.Speaker = best
		}
		merged = append(merged, span)
	}
	return merged
}
