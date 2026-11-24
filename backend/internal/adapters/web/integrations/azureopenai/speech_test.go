package azureopenai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonara/backend/usage"
	"sonara/internal/audio"
)

func TestSpeakUsesAzureDeploymentAndRecordsConsumption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/openai/deployments/interviewer/audio/speech" ||
			r.URL.Query().Get("api-version") != "2025-04-01-preview" ||
			r.Header.Get("api-key") != "example-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unexpected Azure speech request")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["input"] != "¿Qué tal?" || body["voice"] != "alloy" || body["response_format"] != "wav" {
			t.Errorf("invalid body: %v, %v", body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write(audio.WAV(make([]byte, 32000)))
	}))
	defer server.Close()
	g := NewGateway()
	g.SetConfig(Config{Endpoint: server.URL, APIKey: "example-key", SpeechDeployment: "interviewer"})
	var records []usage.Entry
	ctx := usage.With(context.Background(), func(e usage.Entry) { records = append(records, e) }, usage.TTS)
	if wav, err := g.Speak(ctx, "¿Qué tal?", "alloy", ""); err != nil || usage.WAVSeconds(wav) != 1 {
		t.Fatalf("seconds %f, err %v", usage.WAVSeconds(wav), err)
	}
	if len(records) != 1 || records[0].Provider != "azure" || records[0].Model != "interviewer" || records[0].Characters != 9 || !records[0].OK {
		t.Fatalf("%+v", records)
	}
}

func TestSpeakMissingDeploymentFailsBeforeNetwork(t *testing.T) {
	g := NewGateway()
	g.SetConfig(Config{Endpoint: "http://127.0.0.1:1", APIKey: "example-key"})
	_, err := g.Speak(context.Background(), "Hello", "alloy", "")
	if err == nil || !strings.Contains(err.Error(), "deployment de voz") {
		t.Fatalf("got %v", err)
	}
}
