package app

import (
	"context"
	"fmt"
	"time"

	"sonara/internal/audio"
	"sonara/internal/diarize"
	"sonara/internal/storage"
)

const (
	// diarizeMaxDuration caps the accumulated meeting_output audio: past this,
	// further audio is dropped from the diarization pass (the meeting itself
	// keeps transcribing normally; only the extra minutes stay unlabeled).
	// Well under the Files API's stated 2GB per-file cap; chosen as a memory
	// bound for this app, not a Gemini limit.
	diarizeMaxDuration = 3 * time.Hour
	diarizeMaxBytes    = int64(diarizeMaxDuration/time.Second) * audio.SampleRate * 2
)

// --- Accumulating the meeting_output track for the final diarization pass ---

// appendDiarizePCM buffers one frame of the meeting_output track, origin-
// aligned to the recording's shared clock. No-op once diarizeMaxBytes is
// reached: the rest of the meeting keeps transcribing, just without diarized
// names past that point.
func (r *meetingRun) diarizeBuffer(track string) *[]byte {
	if track == storage.TrackMicrophone {
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
	silence := make([]byte, durationMS*audio.SampleRate*2/1000)
	r.appendDiarizePCM(track, silence)
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
	ctx, cancel := context.WithTimeout(context.Background(), diarize.FinalTimeout)
	defer cancel()
	found, failed := 0, false
	for _, track := range []string{storage.TrackMicrophone, storage.TrackOutput} {
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
	a.nameFromMeet(run.sessionID)
	a.nameParticipants(ctx, run.sessionID)
}

// diarizePeriod is how often the participants are re-separated while the
// meeting is still being recorded. A var so tests do not wait minutes.
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
		case <-time.After(a.diarizer.Period()):
		}
		if !a.diarizer.Configured() {
			continue
		}
		found := 0
		for _, track := range []string{storage.TrackMicrophone, storage.TrackOutput} {
			pcm := run.diarizeSnapshot(track)
			if len(pcm)-lastBytes[track] < a.diarizer.MinGrowth() {
				continue
			}
			lastBytes[track] = len(pcm)
			passCtx, cancel := context.WithTimeout(ctx, diarize.FinalTimeout)
			found += max(a.applyDiarization(passCtx, run, track, pcm), 0)
			cancel()
		}
		if found > 0 {
			a.setDiarizeNote(run.sessionID, fmt.Sprintf("Participantes detectados hasta ahora: %d (se actualiza durante la reunión).", found))
			// Names mentioned so far show up during the meeting, not only after
			// it; at most once a minute, since each pass reads the whole text.
			a.nameFromMeet(run.sessionID) // local and cheap: every pass
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
	if track == storage.TrackMicrophone {
		speakers = a.MeetingPeople()
	}
	spans, err := a.diarizer.Diarize(ctx, audio.WAV(pcm), speakers)
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
	spans = diarize.MergeMinorSpeakers(spans)
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return 0
	}
	segments, err := database.TranscriptSegments(run.sessionID)
	if err != nil {
		logf("meeting %s: could not reload segments for diarization: %v", run.sessionID, err)
		return 0
	}
	keys := diarize.StableKeys(spans)
	for _, segment := range segments {
		if segment.Track != track || segment.Status != storage.SegmentOK {
			continue
		}
		raw := diarize.OverlapSpeaker(spans, segment.StartMS, segment.EndMS)
		if raw == "" {
			continue
		}
		key := keys[raw]
		if segment.SpeakerKey == key {
			continue // nothing changed: do not re-announce it
		}
		if err := database.SetSegmentSpeaker(segment.ID, key); err != nil {
			logf("meeting %s: could not store the speaker for segment %d: %v", run.sessionID, segment.ID, err)
			continue
		}
		segment.SpeakerKey = key
		a.emit("meeting:segment", MeetingSegmentEvent{SessionID: run.sessionID, Segment: segment})
	}
	logf("meeting %s: diarization of %s found %d participant(s)", run.sessionID, track, len(keys))
	return len(keys)
}
