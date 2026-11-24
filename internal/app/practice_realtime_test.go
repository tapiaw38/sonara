package app

import (
	"slices"
	"testing"

	"sonara/backend/client"
)

func TestPracticeVoiceQueuesNextAudioItemWhilePreviousDrains(t *testing.T) {
	player := &practiceVoicePlayer{responseID: "response", itemID: "first", finished: true}
	var queue practiceVoiceAudioQueue
	for _, event := range []client.VoiceEvent{
		{Type: "response.output_audio.delta", ResponseID: "response", ItemID: "second", Audio: []byte{1, 2}},
		{Type: "response.output_audio.done", ResponseID: "response", ItemID: "second"},
	} {
		if player.matches(event) {
			t.Fatal("second item matched the first player")
		}
		if err := queue.enqueue(event); err != nil {
			t.Fatal(err)
		}
	}
	var played []string
	canPlay := func(event client.VoiceEvent) bool { return player == nil || player.matches(event) }
	play := func(event client.VoiceEvent) error {
		played = append(played, event.Type)
		if player == nil {
			player = &practiceVoicePlayer{responseID: event.ResponseID, itemID: event.ItemID}
		}
		return nil
	}
	if err := queue.drain(canPlay, play); err != nil || len(played) != 0 {
		t.Fatalf("played while first item was draining: %v, %v", played, err)
	}
	player = nil
	want := []string{"response.output_audio.delta", "response.output_audio.done"}
	if err := queue.drain(canPlay, play); err != nil || !slices.Equal(played, want) || len(queue) != 0 {
		t.Fatalf("played %v, want %v, queue=%d, err=%v", played, want, len(queue), err)
	}
}
