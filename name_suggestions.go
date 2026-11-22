package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// genericSpeakerToken stands in for an empty speaker_key (the undiarized,
// single "Participante" bucket) in the text sent to the model: an LLM given
// a literal blank label as a JSON key is a bad bet, this isn't.
const genericSpeakerToken = "generico"

// micSpeakerTokenPrefix marks a participant separated within the microphone,
// so its token never collides with a meeting_output key.
const micSpeakerTokenPrefix = "mic_"

// buildNameInferenceTranscript lists what was actually said, one line per
// spoken segment, labeled by speaker token. The microphone is always "Yo" —
// context for the model, never something it gets asked to rename.
func buildNameInferenceTranscript(segments []TranscriptSegment) string {
	var out strings.Builder
	for _, segment := range segments {
		if segment.Status != segmentOK || strings.TrimSpace(segment.Text) == "" {
			continue
		}
		token := "Yo"
		if segment.Track == trackMicrophone && segment.SpeakerKey != "" {
			token = micSpeakerTokenPrefix + segment.SpeakerKey
		}
		if segment.Track == trackOutput {
			token = segment.SpeakerKey
			if token == "" {
				token = genericSpeakerToken
			}
		}
		fmt.Fprintf(&out, "%s: %s\n", token, segment.Text)
	}
	return out.String()
}

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
		if segment.Status == segmentOK && segment.Track == trackOutput {
			validKeys[segment.SpeakerKey] = true // "" is a valid key: the generic bucket
		}
	}
	transcript := buildNameInferenceTranscript(view.Segments)
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
		if token == genericSpeakerToken {
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
	segments, err := database.transcriptSegments(sessionID)
	if err != nil {
		return
	}
	transcript := buildNameInferenceTranscript(segments)
	if strings.TrimSpace(transcript) == "" {
		return
	}
	raw, err := backend.SuggestSpeakerNames(ctx, transcript)
	if err != nil {
		logf("meeting %s: naming participants failed: %v", sessionID, err)
		return
	}
	labels, err := database.speakerNames(sessionID)
	if err != nil {
		return
	}
	named := 0
	for token, name := range raw {
		track, key := trackOutput, token
		switch {
		case token == "Yo":
			continue
		case token == genericSpeakerToken:
			key = ""
		case strings.HasPrefix(token, micSpeakerTokenPrefix):
			track, key = trackMicrophone, strings.TrimPrefix(token, micSpeakerTokenPrefix)
		}
		label, ok := labels[speakerDisplayKey(track, key)]
		if !ok || !strings.HasPrefix(label, "Participante") || strings.Contains(label, "(") {
			continue // unknown token, or already named by hand
		}
		if err := database.saveSpeakerName(sessionID, track, key, label+" ("+name+")"); err != nil {
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
