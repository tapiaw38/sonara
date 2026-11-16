package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

func testGateway(url string) *Gateway {
	return NewGateway(config.Config{
		DeepSeekKey: "k", DeepSeekBaseURL: url, DeepSeekModelFast: "flash", DeepSeekModelThink: "pro",
		ReasoningModel: "r", ExpertModel: "e", FastModel: "f", MaxOutputTokens: 100,
	})
}

func TestParseStreamLine(t *testing.T) {
	cases := []struct {
		line   string
		delta  string
		done   bool
		failed string
	}{
		{`data: {"choices":[{"delta":{"content":"hola"}}]}`, "hola", false, ""},
		{`data: {"choices":[{"delta":{"content":" a\nb "}}]}`, " a\nb ", false, ""},
		{`data: [DONE]`, "", true, ""},
		{`data: {"choices":[]}`, "", false, ""},
		{`: keep-alive`, "", false, ""},
		{`data: no-json`, "", false, ""},
		{`data: {"error":{"message":"busy"}}`, "", true, "busy"},
	}
	for _, c := range cases {
		d, done, f := parseStreamLine(c.line)
		if d != c.delta || done != c.done || f != c.failed {
			t.Errorf("%q = (%q,%v,%q), want (%q,%v,%q)", c.line, d, done, f, c.delta, c.done, c.failed)
		}
	}
}

func TestReadStreamRequiresDone(t *testing.T) {
	got, err := readStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: [DONE]\n"), nil)
	if err != nil || got != "a" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := readStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n"), nil); err == nil {
		t.Fatal("truncated stream must fail")
	}
}

func TestRespondMapsTierAndSystemPrompt(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	g := testGateway(srv.URL)
	if text, err := g.Respond(context.Background(), domain.Request{Prompt: "p"}, "r", "sys"); err != nil || text != "ok" {
		t.Fatalf("got %q, %v", text, err)
	}
	if got.Model != "pro" || got.Messages[0].Role != "system" || got.Messages[0].Content != "sys" || got.Messages[1].Content != "p" {
		t.Fatalf("unexpected request %+v", got)
	}
	g.Respond(context.Background(), domain.Request{Prompt: "p"}, "f", "sys")
	if got.Model != "flash" {
		t.Fatalf("fast tier should map to flash, got %q", got.Model)
	}
}

func TestRespondRejectsImages(t *testing.T) {
	if _, err := testGateway("http://unused").Respond(context.Background(), domain.Request{ImageData: "x"}, "f", ""); err == nil {
		t.Fatal("images must never be sent to DeepSeek")
	}
}

func TestTranscribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/asr" {
			t.Errorf("path %s", r.URL.Path)
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Errorf("missing file field: %v", err)
		}
		w.Write([]byte(`{"request_id":"r1","text":"hola mundo","duration_ms":3000}`))
	}))
	defer srv.Close()
	out, err := testGateway(srv.URL).Transcribe(context.Background(), make([]byte, 1000), "a.wav", "es")
	if err != nil || out.Text != "hola mundo" || out.RequestID != "r1" || out.Status != 200 {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestCheckASRAudio(t *testing.T) {
	if err := checkASRAudio(make([]byte, wavHeaderBytes+asrBytesPerSecond*20)); err != nil {
		t.Fatalf("20s block rejected: %v", err)
	}
	if err := checkASRAudio(make([]byte, wavHeaderBytes+asrBytesPerSecond*121)); err == nil {
		t.Fatal("121s block accepted")
	}
	if err := checkASRAudio(make([]byte, maxASRBytes+1)); err == nil {
		t.Fatal("oversized block accepted")
	}
}

func TestNotConfigured(t *testing.T) {
	g := NewGateway(config.Config{})
	if _, err := g.Transcribe(context.Background(), nil, "", ""); err != ErrNotConfigured {
		t.Fatalf("got %v", err)
	}
}
