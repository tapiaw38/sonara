package realtime

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestEndpointFor(t *testing.T) {
	cases := map[string]string{
		"https://api.openai.com/v1":  "wss://api.openai.com/v1/realtime?intent=transcription",
		"https://api.openai.com/v1/": "wss://api.openai.com/v1/realtime?intent=transcription",
		"http://localhost:9000/v1":   "ws://localhost:9000/v1/realtime?intent=transcription",
	}
	for in, want := range cases {
		if got, err := EndpointFor(in); err != nil || got != want {
			t.Errorf("EndpointFor(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := EndpointFor("::nope"); err == nil {
		t.Error("an invalid base URL must error")
	}
}

func TestResamplerRatioAndContinuity(t *testing.T) {
	var r resampler
	frame := make([]byte, 1600*2) // 100 ms at 16 kHz
	for i := 0; i < 1600; i++ {
		binary.LittleEndian.PutUint16(frame[i*2:], uint16(int16(1000))) // DC signal
	}
	total := 0
	for i := 0; i < 10; i++ {
		out := r.convert(frame)
		total += len(out) / 2
		for j := 0; j+1 < len(out); j += 2 {
			if v := int16(binary.LittleEndian.Uint16(out[j:])); v < 990 || v > 1010 {
				t.Fatalf("DC level drifted to %d: resampling must keep the signal", v)
			}
		}
	}
	// 1 s of 16 kHz audio in -> 1 s of 24 kHz audio out (±1 sample of carry).
	if total < 23999 || total > 24001 {
		t.Errorf("1s of audio became %d samples, want 24000", total)
	}
}

type fakeServer struct {
	t        *testing.T
	auth     chan http.Header // the handshake's headers, one per connection
	received chan map[string]any
	send     chan map[string]any
}

func newFakeServer(t *testing.T) (*fakeServer, string, func()) {
	fs := &fakeServer{t: t, auth: make(chan http.Header, 4), received: make(chan map[string]any, 16), send: make(chan map[string]any, 16)}
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.auth <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		go func() {
			for msg := range fs.send {
				conn.WriteJSON(msg)
			}
			conn.Close() // stop() hangs up, like a server going away
		}()
		for {
			var m map[string]any
			if conn.ReadJSON(&m) != nil {
				return
			}
			fs.received <- m
		}
	}))
	return fs, "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/realtime?intent=transcription", func() { close(fs.send); srv.Close() }
}

func next(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
		return nil
	}
}

func TestSessionProtocol(t *testing.T) {
	fs, url, stop := newFakeServer(t)
	defer stop()
	s, err := Dial(context.Background(), Options{URL: url, APIKey: "k", Language: "en", Delay: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if header := <-fs.auth; header.Get("Authorization") != "Bearer k" {
		t.Errorf("Authorization = %q", header.Get("Authorization"))
	}
	update := next(t, fs.received)
	raw, _ := json.Marshal(update)
	for _, want := range []string{
		`"type":"session.update"`, `"type":"transcription"`, `"model":"gpt-live-transcribe"`,
		`"languages":["en"]`, `"delay":"minimal"`, `"rate":24000`, `"turn_detection":null`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("session.update lacks %s: %s", want, raw)
		}
	}

	if err := s.Append(make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	appendMsg := next(t, fs.received)
	audio, _ := base64.StdEncoding.DecodeString(appendMsg["audio"].(string))
	if appendMsg["type"] != "input_audio_buffer.append" || len(audio) != 4800 {
		t.Errorf("append = %v, %d bytes of audio (want 4800: 1600 samples x1.5 x2 bytes)", appendMsg["type"], len(audio))
	}
	s.Commit()
	if m := next(t, fs.received); m["type"] != "input_audio_buffer.commit" {
		t.Errorf("commit message = %v", m)
	}

	fs.send <- map[string]any{"type": "conversation.item.input_audio_transcription.delta", "item_id": "i1", "delta": "Hel"}
	fs.send <- map[string]any{"type": "input_audio_buffer.committed", "item_id": "i1"}
	fs.send <- map[string]any{"type": "conversation.item.input_audio_transcription.completed", "item_id": "i1", "transcript": "Hello"}
	fs.send <- map[string]any{"type": "error", "error": map[string]any{"code": "input_audio_buffer_commit_empty", "message": "too small"}}
	fs.send <- map[string]any{"type": "session.updated"} // ignored

	want := []Event{{Kind: Delta, ItemID: "i1", Text: "Hel"}, {Kind: Committed, ItemID: "i1"}, {Kind: Completed, ItemID: "i1", Text: "Hello"}}
	for _, w := range want {
		select {
		case got := <-s.Events():
			if got.Kind != w.Kind || got.ItemID != w.ItemID || got.Text != w.Text {
				t.Errorf("event = %+v, want %+v", got, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no event for %+v", w)
		}
	}
	failed := <-s.Events()
	if failed.Kind != Failed || !strings.Contains(failed.Err.Error(), "input_audio_buffer_commit_empty") {
		t.Errorf("error event = %+v", failed)
	}
}

func TestClosedIsTheLastEvent(t *testing.T) {
	_, url, stop := newFakeServer(t)
	s, err := Dial(context.Background(), Options{URL: url, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	stop() // server goes away
	select {
	case ev, ok := <-s.Events():
		if !ok || ev.Kind != Closed {
			t.Fatalf("first event after the server vanished = %+v (open=%v), want Closed", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Closed event")
	}
	if _, ok := <-s.Events(); ok {
		t.Error("channel must close after Closed")
	}
}

func TestDialWithoutKey(t *testing.T) {
	if _, err := Dial(context.Background(), Options{URL: "ws://x"}); err == nil {
		t.Fatal("a missing key must fail before dialing")
	}
}

// Azure: api-key header, the deployment as model, and a single language with
// no delay, which gpt-4o-*-transcribe rejects (failing the whole update).
func TestAzureSession(t *testing.T) {
	fs, url, stop := newFakeServer(t)
	defer stop()
	s, err := Dial(context.Background(), Options{URL: url, APIKey: "k", AzureKey: true, Model: "gpt-4o-mini-transcribe", Language: "es", Delay: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	header := <-fs.auth
	if header.Get("api-key") != "k" || header.Get("Authorization") != "" {
		t.Errorf("headers = %v, want only api-key", header)
	}
	raw, _ := json.Marshal(next(t, fs.received))
	for _, want := range []string{`"model":"gpt-4o-mini-transcribe"`, `"language":"es"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("session.update lacks %s: %s", want, raw)
		}
	}
	for _, unwanted := range []string{`"delay"`, `"languages"`} {
		if strings.Contains(string(raw), unwanted) {
			t.Errorf("session.update must not send %s to Azure: %s", unwanted, raw)
		}
	}
}

func TestAzureEndpointFor(t *testing.T) {
	got, err := AzureEndpointFor("https://copilot.openai.azure.com/")
	if err != nil || got != "wss://copilot.openai.azure.com/openai/v1/realtime?intent=transcription" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := AzureEndpointFor("not a url"); err == nil {
		t.Fatal("an invalid endpoint must error")
	}
}

// The first handshake dies mid-way (like Azure's EOF after idle): Dial must
// retry and connect. A 401 must not be retried.
func TestDialRetriesATransientHandshakeFailure(t *testing.T) {
	saved := dialRetryDelays
	dialRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { dialRetryDelays = saved })

	var attempts atomic.Int32
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close() // EOF before the upgrade answer
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var m map[string]any
		for conn.ReadJSON(&m) == nil {
		}
	}))
	defer srv.Close()
	s, err := Dial(context.Background(), Options{URL: "ws" + strings.TrimPrefix(srv.URL, "http"), APIKey: "k"})
	if err != nil {
		t.Fatalf("a dropped first handshake must be retried: %v", err)
	}
	s.Close()
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}

	attempts.Store(0)
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer denied.Close()
	if _, err := Dial(context.Background(), Options{URL: "ws" + strings.TrimPrefix(denied.URL, "http"), APIKey: "k"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the 401", err)
	}
	if attempts.Load() != 1 {
		t.Errorf("a 401 was retried: %d attempts", attempts.Load())
	}
}

// A gpt-live-transcribe deployment on Azure gets that model's languages list,
// but never delay, which Azure rejects for it too.
func TestAzureLiveModelSession(t *testing.T) {
	fs, url, stop := newFakeServer(t)
	defer stop()
	s, err := Dial(context.Background(), Options{URL: url, APIKey: "k", AzureKey: true, Model: "my-live", LiveModel: true, Language: "es", Delay: "minimal"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	<-fs.auth
	raw, _ := json.Marshal(next(t, fs.received))
	if !strings.Contains(string(raw), `"languages":["es"]`) || !strings.Contains(string(raw), `"model":"my-live"`) || strings.Contains(string(raw), `"delay"`) {
		t.Errorf("session.update = %s", raw)
	}
}
