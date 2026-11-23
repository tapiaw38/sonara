package diarize

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"sonara/internal/audio"
)

func TestOverlapSpeakerPicksMostTimeInWindow(t *testing.T) {
	spans := []Span{
		{StartMS: 0, EndMS: 4000, Speaker: "A"},
		{StartMS: 3000, EndMS: 9000, Speaker: "B"}, // overlaps A by 1s, then alone for 5s
	}
	if got := OverlapSpeaker(spans, 0, 9000); got != "B" {
		t.Errorf("got %q, want B (6s vs A's 4s)", got)
	}
	if got := OverlapSpeaker(spans, 0, 2000); got != "A" {
		t.Errorf("a window fully inside A's span got %q", got)
	}
	if got := OverlapSpeaker(nil, 0, 1000); got != "" {
		t.Errorf("no spans must yield no speaker, got %q", got)
	}
	if got := OverlapSpeaker(spans, 20000, 21000); got != "" {
		t.Errorf("a window outside every span must yield no speaker, got %q", got)
	}
}

func TestStableSpeakerKeysOrderedByFirstAppearance(t *testing.T) {
	// Out of chronological order on purpose: the raw labels a diarization call
	// returns are not sorted, and B must still be numbered after A because A
	// spoke first.
	spans := []Span{
		{StartMS: 5000, EndMS: 6000, Speaker: "B"},
		{StartMS: 0, EndMS: 1000, Speaker: "A"},
		{StartMS: 2000, EndMS: 3000, Speaker: "A"},
		{StartMS: 7000, EndMS: 8000, Speaker: "C"},
	}
	keys := StableKeys(spans)
	if keys["A"] != "spk_0" || keys["B"] != "spk_1" || keys["C"] != "spk_2" {
		t.Errorf("keys = %v, want A=spk_0 B=spk_1 C=spk_2", keys)
	}
}

func TestDiarizeHappyPath(t *testing.T) {
	d := fakeGemini(t, []Span{{StartMS: 0, EndMS: 1000, Speaker: "Speaker 1"}}, http.StatusOK, "")
	spans, err := d.Diarize(context.Background(), audio.WAV(make([]byte, audio.SampleRate)), 0)
	want := []Span{{StartMS: 0, EndMS: 500, Speaker: "Speaker 1"}}
	if err != nil || !slices.Equal(spans, want) {
		t.Fatalf("spans = %+v, err = %v", spans, err)
	}
}

func TestDiarizeNoKeyConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no local sherpa-onnx either
	d := NewService(NewGemini(nil, GeminiBaseURL, GeminiUploadURL))
	if d.Configured() {
		t.Fatal("a fresh diarizer must not be configured")
	}
	if _, err := d.Diarize(context.Background(), nil, 0); err == nil {
		t.Fatal("calling without a key must error")
	}
}

func TestDiarizePropagatesInteractionFailure(t *testing.T) {
	d := fakeGemini(t, nil, http.StatusInternalServerError, "")
	if _, err := d.Diarize(context.Background(), []byte("x"), 0); err == nil {
		t.Fatal("a failing interaction must error")
	}
}

func TestDiarizeRejectsMalformedModelOutput(t *testing.T) {
	d := fakeGemini(t, nil, http.StatusOK, "not a json array")
	if _, err := d.Diarize(context.Background(), []byte("x"), 0); err == nil {
		t.Fatal("non-JSON model output must error, not silently produce zero speakers")
	}
}

func TestParseSherpaSpans(t *testing.T) {
	output := "progress 100.00%\nStarted\n1.583 -- 3.406 speaker_00\n9.346 -- 11.472 speaker_01\nDuration : 16.000 s\n"
	spans := parseSherpaSpans(output)
	if len(spans) != 2 || spans[0] != (Span{StartMS: 1583, EndMS: 3406, Speaker: "speaker_00"}) ||
		spans[1].Speaker != "speaker_01" || spans[1].StartMS != 9346 {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestMergeMinorSpeakers(t *testing.T) {
	spans := []Span{
		{StartMS: 0, EndMS: 10000, Speaker: "a"},
		{StartMS: 10000, EndMS: 10800, Speaker: "ghost"}, // 0.8s: not a person
		{StartMS: 11000, EndMS: 20000, Speaker: "b"},
		{StartMS: 19500, EndMS: 30000, Speaker: "b"},
	}
	got := MergeMinorSpeakers(spans)
	seen := map[string]bool{}
	for _, span := range got {
		seen[span.Speaker] = true
	}
	if len(seen) != 2 || seen["ghost"] || got[1].Speaker != "a" {
		t.Fatalf("got %+v; want ghost merged into its nearest real speaker", got)
	}
}

func TestIsEcho(t *testing.T) {
	spans := []EchoSpan{{StartMS: 0, EndMS: 4000}, {StartMS: 6000, EndMS: 7000}}
	cases := []struct {
		name           string
		startMS, endMS int64
		want           bool
	}{
		{"fully covered", 0, 4000, true},
		{"mostly covered (3/4)", 1000, 5000, true},
		// Window [3000,8000): 1000ms overlaps span 1 (3000-4000), 1000ms
		// overlaps span 2 (6000-7000) = 2000/5000 = 0.4, under the threshold.
		{"under threshold (2/5)", 3000, 8000, false},
		{"no overlap at all", 10000, 11000, false},
		{"zero duration", 1000, 1000, false},
		{"empty spans", 0, 1000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spans := spans
			if c.name == "empty spans" {
				spans = nil
			}
			if got := IsEcho(spans, c.startMS, c.endMS); got != c.want {
				t.Errorf("IsEcho(%d,%d) = %v, want %v", c.startMS, c.endMS, got, c.want)
			}
		})
	}
}

func TestIsEchoCountsOverlapsOnce(t *testing.T) {
	spans := []EchoSpan{{StartMS: 100, EndMS: 400}, {StartMS: 0, EndMS: 350}}
	if IsEcho(spans, 0, 1000) {
		t.Fatal("overlapping spans cover only 400 of 1000 ms")
	}
}

func TestCleanSpans(t *testing.T) {
	spans := []Span{
		{StartMS: -1, EndMS: 50, Speaker: "A"},
		{StartMS: 0, EndMS: -1, Speaker: "A"},
		{StartMS: 50, EndMS: 50, Speaker: "A"},
		{StartMS: 60, EndMS: 50, Speaker: "A"},
		{StartMS: 0, EndMS: 50},
		{StartMS: 0, EndMS: 50, Speaker: " \t"},
		{StartMS: 100, EndMS: 150, Speaker: "A"},
		{StartMS: 0, EndMS: 40, Speaker: "A"},
		{StartMS: 40, EndMS: 150, Speaker: "B"},
	}
	want := []Span{{StartMS: 0, EndMS: 40, Speaker: "A"}, {StartMS: 40, EndMS: 100, Speaker: "B"}}
	if got := cleanSpans(spans, 100); !slices.Equal(got, want) {
		t.Fatalf("cleanSpans = %+v, want %+v", got, want)
	}
}

func TestCleanEchoSpans(t *testing.T) {
	spans := []EchoSpan{
		{StartMS: -1, EndMS: 50},
		{StartMS: 0, EndMS: -1},
		{StartMS: 50, EndMS: 50},
		{StartMS: 60, EndMS: 50},
		{StartMS: 100, EndMS: 150},
		{StartMS: 0, EndMS: 40},
		{StartMS: 40, EndMS: 150},
	}
	want := []EchoSpan{{StartMS: 0, EndMS: 40}, {StartMS: 40, EndMS: 100}}
	if got := cleanEchoSpans(spans, 100); !slices.Equal(got, want) {
		t.Fatalf("cleanEchoSpans = %+v, want %+v", got, want)
	}
}

// fakeGeminiEcho mirrors fakeGemini (diarize_test.go) but answers the
// /v1beta/interactions call with echo spans instead of speaker spans; it
// reuses the exact same fake upload endpoints.
func fakeGeminiEcho(t *testing.T, spans []EchoSpan, status int) *Service {
	t.Helper()
	encoded, _ := json.Marshal(spans)
	return fakeGeminiRaw(t, status, string(encoded))
}

func TestDetectEchoHappyPath(t *testing.T) {
	d := fakeGeminiEcho(t, []EchoSpan{{StartMS: 1000, EndMS: 2000}}, http.StatusOK)
	spans, err := d.DetectEcho(context.Background(), []byte("sample"), audio.WAV(make([]byte, 3*audio.SampleRate)))
	want := []EchoSpan{{StartMS: 1000, EndMS: 1500}}
	if err != nil || !slices.Equal(spans, want) {
		t.Fatalf("spans = %+v, err = %v", spans, err)
	}
}

func TestDetectEchoPropagatesFailure(t *testing.T) {
	d := fakeGeminiEcho(t, nil, http.StatusInternalServerError)
	if _, err := d.DetectEcho(context.Background(), []byte("s"), []byte("m")); err == nil {
		t.Fatal("a failing interaction must error")
	}
}

func TestDetectEchoNoKeyConfigured(t *testing.T) {
	d := NewService(NewGemini(nil, GeminiBaseURL, GeminiUploadURL))
	if _, err := d.DetectEcho(context.Background(), nil, nil); err == nil {
		t.Fatal("calling without a key must error")
	}
}

// fakeGemini stubs the three HTTP calls diarize() makes: start the resumable
// upload, receive the bytes, and answer the interaction. Good enough to test
// the whole diarizer without a real Gemini account.
func fakeGemini(t *testing.T, spans []Span, interactionStatus int, rawOutput string) *Service {
	t.Helper()
	if rawOutput == "" {
		encoded, _ := json.Marshal(spans)
		rawOutput = string(encoded)
	}
	return fakeGeminiRaw(t, interactionStatus, rawOutput)
}

// fakeGeminiRaw is the shared fake behind both fakeGemini (diarization) and
// fakeGeminiEcho (voicematch_test.go): the three HTTP calls diarize()/
// detectEcho() make — start the resumable upload, receive the bytes, and
// answer the interaction with whatever JSON text the caller wants echoed
// back as the model's output.
func fakeGeminiRaw(t *testing.T, interactionStatus int, rawOutput string) *Service {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/v1beta/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-goog-upload-url", "http://"+r.Host+"/upload/bytes")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/upload/bytes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"file": map[string]string{"uri": "https://files/abc", "name": "files/abc"}})
	})
	mux.HandleFunc("/v1beta/interactions", func(w http.ResponseWriter, r *http.Request) {
		if interactionStatus != http.StatusOK {
			w.WriteHeader(interactionStatus)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"steps": []map[string]any{
				{"type": "model_output", "content": []map[string]string{{"type": "text", "text": rawOutput}}},
			},
		})
	})
	mux.HandleFunc("/v1beta/files/abc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	d := NewService(NewGemini(srv.Client(), srv.URL+"/v1beta", srv.URL+"/upload/v1beta/files"))
	d.SetAPIKey("test-key")
	return d
}
