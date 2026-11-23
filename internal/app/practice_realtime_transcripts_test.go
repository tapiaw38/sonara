package app

import (
	"context"
	"slices"
	"testing"

	"sonara/backend/client"
	"sonara/internal/storage"
)

func TestPracticeVoiceHistoryOrdersLateCaptionsAndOmitsInterruptedQuestion(t *testing.T) {
	a := NewApp()
	var interrupted []uint64
	a.emitHook = func(event string, data any) {
		switch event {
		case "practice:interrupted":
			interrupted = append(interrupted, data.(uint64))
		case "practice:discard":
			t.Error("a voice interruption must keep text already displayed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	// History still drains on stop, while asynchronous help is canceled.
	cancel()
	turns := practiceVoiceTranscripts{app: a, ctx: ctx, language: "en", generation: a.sessionGeneration}
	committed := "input_audio_buffer.committed"
	created := "response.created"
	inputDone := "conversation.item.input_audio_transcription.completed"
	outputDone := "response.output_audio_transcript.done"
	for _, event := range []client.VoiceEvent{
		{Type: committed, ItemID: "one"},
		{Type: created, ResponseID: "r1"},
		{Type: outputDone, ResponseID: "r1", Text: "A question played in full"},
	} {
		turns.receive(event)
	}
	turns.played("r1")
	for _, event := range []client.VoiceEvent{
		{Type: "input_audio_buffer.speech_started", ItemID: "two"},
		{Type: committed, ItemID: "two"},
		{Type: created, ResponseID: "r2"},
		{Type: inputDone, ItemID: "two", Text: "Second answer"},
		{Type: outputDone, ResponseID: "r2", Text: "Question interrupted before the end"},
		{Type: "input_audio_buffer.speech_started", ItemID: "three"},
	} {
		turns.receive(event)
	}
	if !slices.Equal(interrupted, []uint64{2}) {
		t.Fatalf("interrupted turns = %v, want [2]", interrupted)
	}
	if len(a.sessionHistory) != 0 {
		t.Fatal("later turns were saved before the first transcript arrived")
	}
	turns.receive(client.VoiceEvent{Type: inputDone, ItemID: "one", Text: "First answer"})
	want := []storage.Message{
		{Role: "user", Text: "First answer"},
		{Role: "assistant", Text: "A question played in full"},
		{Role: "user", Text: "Second answer"},
	}
	if !slices.Equal(a.sessionHistory, want) {
		t.Fatalf("history = %+v, want %+v", a.sessionHistory, want)
	}
}
