package azureopenai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"unicode/utf8"

	"sonara/backend/usage"
)

// Azure's deployment-scoped speech endpoint documents this version:
// https://learn.microsoft.com/azure/ai-services/openai/reference-preview
const speechAPIVersion = "2025-04-01-preview"

func (g *Gateway) CheckSpeech() error { return checkSpeech(g.config()) }

func checkSpeech(cfg Config) error {
	if cfg.SpeechDeployment == "" {
		return fmt.Errorf("falta el deployment de voz de Azure OpenAI en Configuración → Claves")
	}
	if cfg.Endpoint == "" || cfg.APIKey == "" {
		return fmt.Errorf("falta el endpoint o la clave de Azure OpenAI para voz")
	}
	return nil
}

func (g *Gateway) Speak(ctx context.Context, text, voice string, speed float64) (_ []byte, callErr error) {
	cfg := g.config()
	if err := checkSpeech(cfg); err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Input  string  `json:"input"`
		Voice  string  `json:"voice"`
		Format string  `json:"response_format"`
		Speed  float64 `json:"speed"`
	}{text, voice, "wav", speed})
	if err != nil {
		return nil, err
	}
	endpoint := cfg.Endpoint + "/openai/deployments/" + url.PathEscape(cfg.SpeechDeployment) + "/audio/speech?api-version=" + speechAPIVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("api-key", cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	meter := usage.Start(ctx, "azure", cfg.SpeechDeployment, usage.TTS)
	meter.Characters = int64(utf8.RuneCountInString(text))
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("la voz de Azure OpenAI falló (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	meter.AudioSeconds = usage.WAVSeconds(data)
	if meter.AudioSeconds == 0 {
		return nil, fmt.Errorf("Azure OpenAI no devolvió un WAV válido")
	}
	return data, nil
}
