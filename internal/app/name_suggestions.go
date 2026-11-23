package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sonara/internal/meeting"
	"sonara/internal/storage"
)

// SuggestMeetingSpeakerNames asks an LLM to read a meeting's own text and
// propose real names for participants who introduced themselves or were
// addressed by name — never applied automatically. The caller must confirm
// each suggestion through RenameMeetingSpeaker. The microphone ("Yo") is
// never among the results: its label comes from which device captured it,
// not from anything said.
//
// Every candidate is checked against the speaker keys genuinely present in
// this meeting before being returned, so a model that hallucinates a key (or
// tries to rename "Yo") cannot produce a suggestion the UI would act on.
func (a *App) SuggestMeetingSpeakerNames(sessionID string) (map[string]string, error) {
	view, err := a.MeetingTranscriptSegments(sessionID)
	if err != nil {
		return nil, err
	}
	validKeys := map[string]bool{}
	for _, segment := range view.Segments {
		if segment.Status == storage.SegmentOK && segment.Track == storage.TrackOutput {
			validKeys[segment.SpeakerKey] = true // "" is a valid key: the generic bucket
		}
	}
	transcript := meeting.NameInferenceTranscript(view.Segments)
	if strings.TrimSpace(transcript) == "" {
		return nil, fmt.Errorf("la reunión todavía no tiene texto")
	}
	a.mu.RLock()
	hook, backend := a.suggestNamesHook, a.backend
	a.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var raw map[string]string
	switch {
	case hook != nil:
		raw, err = hook(ctx, transcript)
	case backend != nil:
		raw, err = backend.SuggestSpeakerNames(ctx, transcript)
	default:
		return nil, fmt.Errorf("backend no inicializado")
	}
	if err != nil {
		return nil, err
	}
	suggestions := map[string]string{}
	for token, name := range raw {
		key := token
		if token == meeting.GenericSpeakerToken {
			key = ""
		}
		if !validKeys[key] {
			continue
		}
		suggestions[key] = name
	}
	return suggestions, nil
}

// nameParticipants runs after the final separation: it asks the model for the
// names the conversation itself mentions and turns "Participante B" into
// "Participante B (David)". Best-effort and never over a name the user typed.
func (a *App) nameParticipants(ctx context.Context, sessionID string) {
	a.mu.RLock()
	database, backend := a.store, a.backend
	a.mu.RUnlock()
	if database == nil || backend == nil {
		return
	}
	segments, err := database.TranscriptSegments(sessionID)
	if err != nil {
		return
	}
	transcript := meeting.NameInferenceTranscript(segments)
	if strings.TrimSpace(transcript) == "" {
		return
	}
	raw, err := backend.SuggestSpeakerNames(ctx, transcript)
	if err != nil {
		logf("meeting %s: naming participants failed: %v", sessionID, err)
		return
	}
	labels, err := database.SpeakerNames(sessionID)
	if err != nil {
		return
	}
	named := 0
	for token, name := range raw {
		track, key := storage.TrackOutput, token
		switch {
		case token == "Yo":
			continue
		case token == meeting.GenericSpeakerToken:
			key = ""
		case strings.HasPrefix(token, meeting.MicSpeakerTokenPrefix):
			track, key = storage.TrackMicrophone, strings.TrimPrefix(token, meeting.MicSpeakerTokenPrefix)
		}
		label, ok := labels[storage.SpeakerDisplayKey(track, key)]
		if !ok || !strings.HasPrefix(label, "Participante") || strings.Contains(label, "(") {
			continue // unknown token, or already named by hand
		}
		if err := database.SaveSpeakerName(sessionID, track, key, label+" ("+name+")"); err != nil {
			logf("meeting %s: could not store the name for %s: %v", sessionID, token, err)
			continue
		}
		logf("meeting %s: %s named from the conversation", sessionID, label)
		named++
	}
	if named > 0 {
		a.emit("meeting:speakers", sessionID)
	}
	a.emitMeetingStatus()
}
