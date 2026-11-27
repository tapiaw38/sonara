package realtime

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestVoiceSessionStreamsAndTruncatesAudio(t *testing.T) {
	received := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime" || r.URL.Query().Get("model") != "gpt-realtime-2.1-mini" ||
			r.URL.Query().Has("intent") || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("unexpected Realtime handshake")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for _, events := range [][]map[string]any{
			{{"type": "session.updated"}},
			{
				{"type": "input_audio_buffer.committed", "item_id": "candidate-1"},
				{"type": "conversation.item.input_audio_transcription.completed", "item_id": "candidate-1", "transcript": "Candidate answer"},
				{"type": "response.output_audio_transcript.done", "item_id": "assistant-1", "response_id": "response-1", "transcript": "Next question"},
				{"type": "response.output_audio.delta", "item_id": "assistant-1", "response_id": "response-1", "content_index": 0, "delta": "AQI="},
				{"type": "input_audio_buffer.speech_started", "item_id": "candidate-2"},
			},
			nil,
			nil,
		} {
			var message map[string]any
			if err := conn.ReadJSON(&message); err != nil {
				t.Error(err)
				return
			}
			received <- message
			for _, event := range events {
				if err := conn.WriteJSON(event); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := DialVoice(ctx, server.URL+"/v1", "test-key", "Interview in English.", 0.85)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	config := next(t, received)
	body, _ := config["session"].(map[string]any)
	audio, _ := body["audio"].(map[string]any)
	input, _ := audio["input"].(map[string]any)
	output, _ := audio["output"].(map[string]any)
	detection, _ := input["turn_detection"].(map[string]any)
	transcription, _ := input["transcription"].(map[string]any)
	if output["speed"] != 0.85 || config["type"] != "session.update" || body["type"] != "realtime" ||
		transcription["model"] != "gpt-transcribe" || detection["type"] != "semantic_vad" || detection["eagerness"] != "low" ||
		detection["create_response"] != false || detection["interrupt_response"] != true {
		t.Fatalf("unexpected session config: %v", config)
	}
	expectEvent := func(kind string) VoiceEvent {
		t.Helper()
		select {
		case event := <-session.Events():
			if event.Type != kind || event.Err != nil {
				t.Fatalf("event = %+v, want %s", event, kind)
			}
			return event
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", kind)
			return VoiceEvent{}
		}
	}
	expectEvent("session.updated")
	if err := session.Append(make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	appendEvent := next(t, received)
	encoded, _ := appendEvent["audio"].(string)
	pcm, err := base64.StdEncoding.DecodeString(encoded)
	if appendEvent["type"] != "input_audio_buffer.append" || err != nil || len(pcm) < 4796 || len(pcm) > 4800 {
		t.Fatalf("unexpected append event: %v, bytes=%d", appendEvent["type"], len(pcm))
	}
	if item := expectEvent("input_audio_buffer.committed"); item.ItemID != "candidate-1" {
		t.Fatalf("unexpected input commit: %+v", item)
	}
	if caption := expectEvent("conversation.item.input_audio_transcription.completed"); caption.ItemID != "candidate-1" || caption.Text != "Candidate answer" {
		t.Fatalf("unexpected input caption: %+v", caption)
	}
	if caption := expectEvent("response.output_audio_transcript.done"); caption.ResponseID != "response-1" || caption.Text != "Next question" {
		t.Fatalf("unexpected output caption: %+v", caption)
	}
	delta := expectEvent("response.output_audio.delta")
	if delta.ItemID != "assistant-1" || delta.ResponseID != "response-1" || string(delta.Audio) != "\x01\x02" {
		t.Fatalf("unexpected audio delta: %+v", delta)
	}
	expectEvent("input_audio_buffer.speech_started")
	if err := session.Truncate(delta.ItemID, delta.ContentIndex, 120); err != nil {
		t.Fatal(err)
	}
	truncate := next(t, received)
	if truncate["type"] != "conversation.item.truncate" || truncate["item_id"] != "assistant-1" || truncate["audio_end_ms"] != float64(120) {
		t.Fatalf("unexpected truncation: %v", truncate)
	}
	if err := session.SetSpeed(0.5); err != nil {
		t.Fatal(err)
	}
	update := next(t, received)
	body, _ = update["session"].(map[string]any)
	audio, _ = body["audio"].(map[string]any)
	output, _ = audio["output"].(map[string]any)
	if update["type"] != "session.update" || output["speed"] != 0.5 {
		t.Fatalf("unexpected speed update: %v", update)
	}

}
