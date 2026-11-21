package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// Phase 3: detect the user's own voice inside the meeting_output track — an
// echo of themselves played back through someone else's speakers, which
// would otherwise show up as a fake extra "Participante". Same provider and
// same once-at-the-end timing as phase-2 diarization (diarize.go), reusing
// its Gemini client, its accumulated meeting_output buffer, and its upload
// plumbing; this is strictly additive on top of that.
//
// Needs a voluntary voice sample (RecordVoiceSample, voice_sample.go) and a
// Gemini key. Without a sample, this is a silent no-op — nothing about
// recording or transcription depends on it.
//
// *** UNVERIFIED ***: like diarization, this has never run against a real
// Gemini account, and — unlike diarization — the overlap threshold below has
// never been checked against a real echo recording either. The plan
// (PLAN_TRANSCRIPCION_REUNIONES.md) called for validating this kind of
// matching on 3–5 consented recordings before relying on it; that has not
// been done. Treat a "Participante" that disappears as something to verify,
// not something to trust blindly: the underlying text is kept (status
// changes, nothing is deleted), so nothing here is destructive if wrong.

const (
	// echoOverlapFraction: a segment counts as an echo once this much of its
	// own duration is covered by a detected echo span. Picked as a sane
	// starting point (more than half), not calibrated against real audio.
	// ponytail: raise this if real meetings show false positives (another
	// participant with a similar voice wrongly cleared); lower it if real
	// echo is only partially caught.
	echoOverlapFraction = 0.5
)

// echoSpan is a stretch of the meeting_output track where the reference
// voice was heard, in the same absolute clock as transcript_segments.
type echoSpan struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

var echoSchema = map[string]any{
	"type": "array",
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"start_ms": map[string]any{"type": "integer"},
			"end_ms":   map[string]any{"type": "integer"},
		},
		"required": []string{"start_ms", "end_ms"},
	},
}

const echoPrompt = "The first audio is a short voice sample of one specific person. The second audio is a " +
	"separate recording: the other side of a meeting (everyone except that person). Determine whether " +
	"that same person's voice can be heard anywhere in the second audio — for example, played back as an " +
	"echo through someone else's speakers, not someone else who merely sounds similar. Return ONLY a JSON " +
	"array of time ranges, each with start_ms and end_ms (integer milliseconds from the start of the " +
	"SECOND audio), covering every moment that voice is heard there. If it is never heard, return []."

// detectEcho uploads the meeting_output recording (sampleWav stays inline:
// a voice sample is a few hundred KB at most, well under any inline limit)
// and asks whether the reference voice appears in it.
func (d *diarizer) detectEcho(ctx context.Context, sampleWav, meetingWav []byte) ([]echoSpan, error) {
	apiKey := d.key()
	if apiKey == "" {
		return nil, fmt.Errorf("falta la clave de Gemini (Configuración)")
	}
	fileURI, fileName, mimeType, err := d.uploadFile(ctx, apiKey, meetingWav)
	if err != nil {
		return nil, fmt.Errorf("no se pudo subir el audio de la reunión: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.deleteFile(cleanupCtx, apiKey, fileName); err != nil {
			logf("could not delete the meeting audio uploaded to Gemini: %v", err)
		}
	}()
	text, err := d.interact(ctx, apiKey, geminiRequest{
		Model: geminiModelName(),
		Input: []geminiInput{
			{Type: "text", Text: echoPrompt},
			{Type: "audio", Data: base64.StdEncoding.EncodeToString(sampleWav), MimeType: "audio/wav"},
			{Type: "audio", URI: fileURI, MimeType: mimeType},
		},
		ResponseFormat: geminiResponseFormat{Type: "text", MimeType: "application/json", Schema: echoSchema},
	})
	if err != nil {
		return nil, err
	}
	var spans []echoSpan
	if err := json.Unmarshal([]byte(text), &spans); err != nil {
		return nil, fmt.Errorf("Gemini no devolvió la lista de intervalos esperada")
	}
	return spans, nil
}

// isEcho reports whether most of [startMS, endMS) is covered by echo spans.
func isEcho(spans []echoSpan, startMS, endMS int64) bool {
	duration := endMS - startMS
	if duration <= 0 || len(spans) == 0 {
		return false
	}
	var covered int64
	for _, span := range spans {
		covered += overlapMS(span.StartMS, span.EndMS, startMS, endMS)
	}
	return float64(covered)/float64(duration) >= echoOverlapFraction
}

// runVoiceMatch is the final pass: best-effort, reads the same accumulated
// buffer runDiarization does (not released until finishMeeting calls
// releaseDiarizePCM, after both passes). Never fails the meeting.
func (a *App) runVoiceMatch(run *meetingRun) {
	if a.diarizer.key() == "" {
		return // voice matching needs Gemini; the local engine only separates
	}
	sample, err := readVoiceSample()
	if err != nil {
		return // no voluntary sample recorded: this feature is simply off
	}
	pcm := run.diarizeSnapshot(trackOutput)
	if len(pcm) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), diarizeFinalTimeout)
	defer cancel()
	spans, err := a.diarizer.detectEcho(ctx, sample, wavFromPCM(pcm))
	if err != nil {
		logf("meeting %s: echo detection skipped: %v", run.sessionID, err)
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
		logf("meeting %s: could not reload segments for echo detection: %v", run.sessionID, err)
		return
	}
	marked := 0
	for _, segment := range segments {
		if segment.Track != trackOutput || segment.Status != segmentOK {
			continue
		}
		if !isEcho(spans, segment.StartMS, segment.EndMS) {
			continue
		}
		if err := database.setSegmentStatus(segment.ID, segmentEcho); err != nil {
			logf("meeting %s: could not mark segment %d as an echo: %v", run.sessionID, segment.ID, err)
			continue
		}
		segment.Status = segmentEcho
		marked++
		a.emit("meeting:segment", MeetingSegmentEvent{SessionID: run.sessionID, Segment: segment})
	}
	if marked > 0 {
		logf("meeting %s: marked %d segment(s) as an echo of the user's own voice", run.sessionID, marked)
	}
}
