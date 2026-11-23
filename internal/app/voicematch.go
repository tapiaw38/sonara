package app

import (
	"context"

	"sonara/internal/audio"
	"sonara/internal/diarize"
	"sonara/internal/storage"
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
// Gemini account, and — unlike diarization — the overlap threshold (diarize.IsEcho) has
// never been checked against a real echo recording either. The plan
// (PLAN_TRANSCRIPCION_REUNIONES.md) called for validating this kind of
// matching on 3–5 consented recordings before relying on it; that has not
// been done. Treat a "Participante" that disappears as something to verify,
// not something to trust blindly: the underlying text is kept (status
// changes, nothing is deleted), so nothing here is destructive if wrong.

// runVoiceMatch is the final pass: best-effort, reads the same accumulated
// buffer runDiarization does (not released until finishMeeting calls
// releaseDiarizePCM, after both passes). Never fails the meeting.
func (a *App) runVoiceMatch(run *meetingRun) {
	if !a.diarizer.HasCloudKey() {
		return // voice matching needs Gemini; the local engine only separates
	}
	sample, err := readVoiceSample()
	if err != nil {
		return // no voluntary sample recorded: this feature is simply off
	}
	pcm := run.diarizeSnapshot(storage.TrackOutput)
	if len(pcm) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), diarize.FinalTimeout)
	defer cancel()
	spans, err := a.diarizer.DetectEcho(ctx, sample, audio.WAV(pcm))
	if err != nil {
		logf("meeting %s: echo detection failed: %v", run.sessionID, err)
		note := "No se pudo detectar el eco: " + err.Error()
		a.mu.Lock()
		if a.diarizeNoteSession == run.sessionID && a.diarizeNote != "" {
			note = a.diarizeNote + " " + note
		}
		a.diarizeNote, a.diarizeNoteSession = note, run.sessionID
		a.mu.Unlock()
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
	segments, err := database.TranscriptSegments(run.sessionID)
	if err != nil {
		logf("meeting %s: could not reload segments for echo detection: %v", run.sessionID, err)
		return
	}
	marked := 0
	for _, segment := range segments {
		if segment.Track != storage.TrackOutput || segment.Status != storage.SegmentOK {
			continue
		}
		if !diarize.IsEcho(spans, segment.StartMS, segment.EndMS) {
			continue
		}
		if err := database.SetSegmentStatus(segment.ID, storage.SegmentEcho); err != nil {
			logf("meeting %s: could not mark segment %d as an echo: %v", run.sessionID, segment.ID, err)
			continue
		}
		segment.Status = storage.SegmentEcho
		marked++
		a.emit("meeting:segment", MeetingSegmentEvent{SessionID: run.sessionID, Segment: segment})
	}
	if marked > 0 {
		logf("meeting %s: marked %d segment(s) as an echo of the user's own voice", run.sessionID, marked)
	}
}
