package azureopenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonara/backend/internal/domain"
)

func testGateway(url string) *Gateway {
	g := NewGateway()
	g.SetConfig(Config{
		APIKey: "k", Endpoint: url, APIVersion: "2024-06-01",
		ChatDeployment: "chat-deploy", TranscribeDeployment: "transcribe-deploy",
	})
	return g
}

func TestConfiguredChecksAreIndependent(t *testing.T) {
	g := NewGateway()
	if g.Configured() || g.TranscribeConfigured() {
		t.Fatal("a fresh gateway must report both as unconfigured")
	}
	g.SetConfig(Config{APIKey: "k", Endpoint: "https://x", APIVersion: "v", ChatDeployment: "chat"})
	if !g.Configured() {
		t.Error("chat config alone should be enough for Configured")
	}
	if g.TranscribeConfigured() {
		t.Error("TranscribeConfigured must stay false without a transcribe deployment")
	}
}

func TestRespondBuildsPlainTextMessageAndURL(t *testing.T) {
	var gotPath string
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		if r.Header.Get("api-key") != "k" {
			t.Errorf("api-key header = %q", r.Header.Get("api-key"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	g := testGateway(srv.URL)
	text, err := g.Respond(context.Background(), domain.Request{Prompt: "hola"}, "ignored", "sys")
	if err != nil || text != "ok" {
		t.Fatalf("got %q, %v", text, err)
	}
	if gotPath != "/openai/deployments/chat-deploy/chat/completions?api-version=2024-06-01" {
		t.Errorf("path = %q", gotPath)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "sys" {
		t.Fatalf("system message = %+v", got.Messages[0])
	}
	if content, ok := got.Messages[1].Content.(string); !ok || content != "hola" {
		t.Fatalf("a text-only request must send a plain string content, got %#v", got.Messages[1].Content)
	}
}

func TestRespondWithImageSendsContentParts(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	g := testGateway(srv.URL)
	_, err := g.Respond(context.Background(), domain.Request{Prompt: "qué ves", ImageData: "data:image/png;base64,AAAA"}, "ignored", "sys")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got.Messages[1].Content)
	var parts []contentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("an image request must send content parts, got %#v (%v)", got.Messages[1].Content, err)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "image_url" || parts[1].ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Fatalf("parts = %+v", parts)
	}
}

func TestRespondRejectsWhenNotConfigured(t *testing.T) {
	g := NewGateway()
	if _, err := g.Respond(context.Background(), domain.Request{Prompt: "x"}, "m", "i"); err == nil {
		t.Fatal("an unconfigured gateway must refuse to call out")
	}
}

func TestRespondPropagatesAzureErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()
	_, err := testGateway(srv.URL).Respond(context.Background(), domain.Request{Prompt: "x"}, "m", "i")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v, want it to carry Azure's message", err)
	}
}

func TestParseStreamLine(t *testing.T) {
	cases := []struct {
		line, delta, failed string
		done                bool
	}{
		{`data: {"choices":[{"delta":{"content":"hola"}}]}`, "hola", "", false},
		{`data: [DONE]`, "", "", true},
		{`: ping`, "", "", false},
		{`data: {"error":{"message":"busy"}}`, "", "busy", true},
	}
	for _, c := range cases {
		delta, done, failed := parseStreamLine(c.line)
		if delta != c.delta || done != c.done || failed != c.failed {
			t.Errorf("%q = (%q,%v,%q), want (%q,%v,%q)", c.line, delta, done, failed, c.delta, c.done, c.failed)
		}
	}
}

func TestTranscribeSendsNoModelFieldAndUsesDeploymentURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if r.MultipartForm.Value["model"] != nil {
			t.Error("Azure selects the model via the deployment in the URL, not a form field")
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Errorf("missing file field: %v", err)
		}
		w.Write([]byte(`{"text":"hola mundo"}`))
	}))
	defer srv.Close()
	out, err := testGateway(srv.URL).Transcribe(context.Background(), make([]byte, 100), "a.wav", "es")
	if err != nil || out.Text != "hola mundo" {
		t.Fatalf("got %+v, %v", out, err)
	}
	if gotPath != "/openai/deployments/transcribe-deploy/audio/transcriptions?api-version=2024-06-01" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestTranscribeRejectsWhenNotConfigured(t *testing.T) {
	g := NewGateway()
	g.SetConfig(Config{APIKey: "k", Endpoint: "https://x", APIVersion: "v", ChatDeployment: "chat"}) // no transcribe deployment
	if _, err := g.Transcribe(context.Background(), nil, "", ""); err == nil {
		t.Fatal("missing the transcribe deployment must refuse, not fall back to the chat one")
	}
}
