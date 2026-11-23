package app

import (
	"strings"
	"time"

	"sonara/internal/meetbridge"
	"sonara/internal/storage"
)

// meetRecorder adapts App to meetbridge.Recorder. A separate type, not a
// method on App: Wails would bind an exported App method to the frontend.
type meetRecorder struct{ a *App }

// ActiveRecording is the meeting the Meet bridge attaches names to.
func (m meetRecorder) ActiveRecording() (sessionID string, started time.Time, ok bool) {
	a := m.a
	a.mu.RLock()
	run := a.meeting
	a.mu.RUnlock()
	if run == nil || run.stateNow() != meetingRecording {
		return "", time.Time{}, false
	}
	return run.sessionID, run.started, true
}

// startMeetBridge serves the Chrome extension until the app exits.
func (a *App) startMeetBridge() {
	// emitMeetingStatus on a new name: the UI shows who Meet reported.
	meetbridge.Start(meetbridge.Handler(meetRecorder{a}, &a.meetSpeaking, a.emitMeetingStatus))
}

// nameFromMeet turns "Participante 2" into "Participante 2 (Walter)" from what
// the extension saw in Meet. Runs before the LLM naming, which then leaves
// those alone; never touches a name the user typed.
func (a *App) nameFromMeet(sessionID string) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	spans := a.meetSpeaking.Snapshot(sessionID)
	if database == nil || len(spans) == 0 {
		return
	}
	segments, err := database.TranscriptSegments(sessionID)
	if err != nil {
		return
	}
	labels, err := database.SpeakerNames(sessionID)
	if err != nil {
		return
	}
	named := 0
	for key, name := range meetbridge.MatchNames(segments, spans) {
		label, ok := labels[storage.SpeakerDisplayKey(storage.TrackOutput, key)]
		if !ok || !strings.HasPrefix(label, "Participante") || strings.Contains(label, "(") {
			continue
		}
		if err := database.SaveSpeakerName(sessionID, storage.TrackOutput, key, label+" ("+name+")"); err != nil {
			logf("meeting %s: could not store the Meet name for %s: %v", sessionID, key, err)
			continue
		}
		named++
	}
	if named > 0 {
		logf("meeting %s: %d participants named from Google Meet", sessionID, named)
		a.emit("meeting:speakers", sessionID)
	}
}
