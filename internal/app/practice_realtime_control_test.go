package app

import (
	"slices"
	"testing"
	"time"

	"sonara/backend/client"
)

func TestPracticeVoiceControlCombinesHeldSpeechAndRemovesCommands(t *testing.T) {
	var control practiceVoiceControl
	var deleted []string
	deleteInput := func(id string) error {
		deleted = append(deleted, id)
		return nil
	}
	push := func(id, text string) {
		control.receive(client.VoiceEvent{Type: "input_audio_buffer.committed", ItemID: id})
		control.receive(client.VoiceEvent{Type: "conversation.item.input_audio_transcription.completed", ItemID: id, Text: text})
	}
	push("hold", "¡Escuchá!")
	if id, _, err := control.drain(time.Now(), true, deleteInput); id != "" || err != nil || !control.holding {
		t.Fatalf("hold submitted %q, err = %v, holding = %t", id, err, control.holding)
	}
	push("part-1", "I listen to our customers.")
	push("part-2", "That helps us prioritize.")
	if id, _, err := control.drain(time.Now(), true, deleteInput); id != "" || err != nil {
		t.Fatalf("speech submitted during hold: %q, %v", id, err)
	}
	push("ready", "I'm done.")
	if id, _, err := control.drain(time.Now(), false, deleteInput); id != "" || err != nil || control.holding {
		t.Fatalf("ready state while previous response is active: %q, %v, holding=%t", id, err, control.holding)
	}
	id, answer, err := control.drain(time.Now(), true, deleteInput)
	if id != "part-2" || answer != "I listen to our customers. That helps us prioritize." || err != nil {
		t.Fatalf("submission = %q, %q, %v", id, answer, err)
	}
	if second, _, _ := control.drain(time.Now(), true, deleteInput); second != "" {
		t.Fatalf("duplicate submission: %q", second)
	}
	if !slices.Equal(deleted, []string{"hold", "ready"}) {
		t.Fatalf("deleted items = %v", deleted)
	}
}

func TestPracticeVoiceControlWaitsForCurrentSpeech(t *testing.T) {
	var control practiceVoiceControl
	control.receive(client.VoiceEvent{Type: "input_audio_buffer.speech_started", ItemID: "answer"})
	deleteInput := func(string) error { return nil }
	if id, _, _ := control.drain(time.Now(), true, deleteInput); id != "" {
		t.Fatalf("submitted while speaking: %q", id)
	}
	control.receive(client.VoiceEvent{Type: "input_audio_buffer.speech_stopped", ItemID: "answer"})
	control.receive(client.VoiceEvent{Type: "input_audio_buffer.committed", ItemID: "answer"})
	if id, _, _ := control.drain(time.Now(), true, deleteInput); id != "" {
		t.Fatalf("submitted before command recognition completed: %q", id)
	}
	control.receive(client.VoiceEvent{Type: "conversation.item.input_audio_transcription.completed", ItemID: "answer", Text: "The report is ready."})
	if id, _, _ := control.drain(time.Now(), true, deleteInput); id != "answer" {
		t.Fatalf("submission = %q, want answer", id)
	}
}
