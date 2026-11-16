// Package azureopenai talks to an Azure OpenAI Service resource: the same
// underlying models OpenAI serves (GPT-4o/5-class for chat and vision,
// Whisper/gpt-4o-transcribe for audio), reached through Azure's own URL
// shape and header-based auth instead of OpenAI's.
//
// Every setting (key, endpoint, API version, deployment names) is runtime
// configuration set from Settings via SetConfig, never an environment
// variable: this integration exists because the user already has an Azure
// resource and wants to type its details into the app, not into a .env
// file.
//
// *** UNVERIFIED ***: built from Microsoft's current public reference docs
// (learn.microsoft.com/azure/ai-services/openai/reference, fetched
// 2026-11-16), never called against a real Azure OpenAI resource. See
// PLAN_TRANSCRIPCION_REUNIONES.md.
package azureopenai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"sonara/backend/internal/domain"
)

// Config is every Azure-specific setting, saved and changed as one unit: a
// Settings form has one save button, not five.
type Config struct {
	APIKey string
	// Endpoint is the resource URL, e.g. https://my-resource.openai.azure.com
	// (no trailing slash, no path).
	Endpoint string
	// APIVersion is the ?api-version= query parameter Azure requires on every
	// call. Azure revises this independently of the model; there is no
	// universal "latest" value to default to reliably, so Settings must set
	// one explicitly.
	APIVersion string
	// ChatDeployment serves both text and vision: Azure deploys one model
	// per name, so vision only works once a vision-capable model (e.g.
	// GPT-4o) is the model deployed under this name.
	ChatDeployment       string
	TranscribeDeployment string
}

type Gateway struct {
	client *http.Client
	mu     sync.RWMutex
	cfg    Config
}

func NewGateway() *Gateway {
	return &Gateway{client: &http.Client{Timeout: 90 * time.Second}}
}

func (g *Gateway) SetConfig(cfg Config) {
	g.mu.Lock()
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.APIVersion = strings.TrimSpace(cfg.APIVersion)
	cfg.ChatDeployment = strings.TrimSpace(cfg.ChatDeployment)
	cfg.TranscribeDeployment = strings.TrimSpace(cfg.TranscribeDeployment)
	g.cfg = cfg
	g.mu.Unlock()
}

func (g *Gateway) config() Config {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg
}

// Configured covers chat and vision (one deployment serves both).
func (g *Gateway) Configured() bool {
	c := g.config()
	return c.APIKey != "" && c.Endpoint != "" && c.APIVersion != "" && c.ChatDeployment != ""
}

// TranscribeConfigured is separate from Configured: a resource may have a
// chat deployment but no transcription one, or vice versa.
func (g *Gateway) TranscribeConfigured() bool {
	c := g.config()
	return c.APIKey != "" && c.Endpoint != "" && c.APIVersion != "" && c.TranscribeDeployment != ""
}

type chatMessage struct {
	Role string `json:"role"`
	// Content is a string for a text-only message, or []contentPart when the
	// request carries an image — Azure OpenAI's chat completions endpoint
	// accepts both shapes the same way OpenAI's own API does.
	Content any `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatRequest struct {
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
}

type chatResult struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func userContent(in domain.Request) any {
	if in.ImageData == "" {
		return in.Prompt
	}
	return []contentPart{
		{Type: "text", Text: in.Prompt},
		{Type: "image_url", ImageURL: &imageURL{URL: in.ImageData}},
	}
}

func (g *Gateway) chatURL(cfg Config) string {
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		cfg.Endpoint, cfg.ChatDeployment, cfg.APIVersion)
}

func (g *Gateway) newChatRequest(ctx context.Context, in domain.Request, instructions string, stream bool) (*http.Request, Config, error) {
	cfg := g.config()
	if !g.Configured() {
		return nil, cfg, fmt.Errorf("Azure OpenAI no está configurado")
	}
	body, err := json.Marshal(chatRequest{
		Messages: []chatMessage{
			{Role: "system", Content: instructions},
			{Role: "user", Content: userContent(in)},
		},
		Stream: stream,
	})
	if err != nil {
		return nil, cfg, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.chatURL(cfg), bytes.NewReader(body))
	if err != nil {
		return nil, cfg, err
	}
	req.Header.Set("api-key", cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, cfg, nil
}

func failure(status int, body []byte) error {
	var result chatResult
	if json.Unmarshal(body, &result) == nil && result.Error != nil && result.Error.Message != "" {
		return fmt.Errorf("Azure OpenAI request failed (%d): %s", status, result.Error.Message)
	}
	return fmt.Errorf("Azure OpenAI request failed with status %d", status)
}

// Respond ignores model: Azure's deployment, not a model string in the
// request body, picks which model answers.
func (g *Gateway) Respond(ctx context.Context, in domain.Request, _ string, instructions string) (string, error) {
	req, _, err := g.newChatRequest(ctx, in, instructions, false)
	if err != nil {
		return "", err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", failure(resp.StatusCode, body)
	}
	var result chatResult
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("Azure OpenAI returned an invalid response")
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("Azure OpenAI returned an empty response")
	}
	return result.Choices[0].Message.Content, nil
}

func parseStreamLine(line string) (delta string, done bool, failureMessage string) {
	payload, found := strings.CutPrefix(line, "data:")
	if !found {
		return "", false, ""
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return "", false, ""
	}
	if payload == "[DONE]" {
		return "", true, ""
	}
	var result chatResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return "", false, ""
	}
	if result.Error != nil {
		message := result.Error.Message
		if message == "" {
			message = "Azure OpenAI stream failed"
		}
		return "", true, message
	}
	if len(result.Choices) == 0 {
		return "", false, ""
	}
	return result.Choices[0].Delta.Content, false, ""
}

func readStream(body io.Reader, onDelta func(string)) (string, error) {
	var text strings.Builder
	completed := false
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		delta, done, failed := parseStreamLine(scanner.Text())
		if failed != "" {
			return text.String(), fmt.Errorf("Azure OpenAI request failed: %s", failed)
		}
		if delta != "" {
			text.WriteString(delta)
			if onDelta != nil {
				onDelta(delta)
			}
		}
		if done {
			completed = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return text.String(), err
	}
	if !completed {
		return text.String(), fmt.Errorf("Azure OpenAI stream ended before [DONE]")
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("Azure OpenAI returned an empty response")
	}
	return text.String(), nil
}

func (g *Gateway) RespondStream(ctx context.Context, in domain.Request, _ string, instructions string, onDelta func(string)) (string, error) {
	req, _, err := g.newChatRequest(ctx, in, instructions, true)
	if err != nil {
		return "", err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return "", failure(resp.StatusCode, body)
	}
	return readStream(io.LimitReader(resp.Body, 8<<20), onDelta)
}

// Transcribe uploads to Azure's audio/transcriptions endpoint for the
// configured deployment. Diagnostics mirror the OpenAI/DeepSeek gateways:
// a slow upload and a slow queue on Azure's side look identical from a
// single duration, so they are kept apart.
func (g *Gateway) Transcribe(ctx context.Context, audio []byte, filename, language string) (domain.Transcript, error) {
	cfg := g.config()
	if !g.TranscribeConfigured() {
		return domain.Transcript{}, fmt.Errorf("Azure OpenAI (transcripción) no está configurado")
	}
	body, contentType, err := transcriptionBody(audio, filename, language)
	if err != nil {
		return domain.Transcript{}, err
	}
	out := domain.Transcript{UploadBytes: body.Len()}
	url := fmt.Sprintf("%s/openai/deployments/%s/audio/transcriptions?api-version=%s",
		cfg.Endpoint, cfg.TranscribeDeployment, cfg.APIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return out, err
	}
	req.Header.Set("api-key", cfg.APIKey)
	req.Header.Set("Content-Type", contentType)

	started := time.Now()
	resp, err := g.client.Do(req)
	out.WaitMS = int(time.Since(started).Milliseconds())
	if err != nil {
		out.TotalMS = out.WaitMS
		return out, err
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	out.RequestID = resp.Header.Get("x-request-id")
	if ms, err := strconv.Atoi(resp.Header.Get("openai-processing-ms")); err == nil {
		out.ProcessingMS = ms
	}
	resultBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	out.TotalMS = int(time.Since(started).Milliseconds())
	if err != nil {
		return out, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, failure(resp.StatusCode, resultBody)
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resultBody, &result); err != nil {
		return out, fmt.Errorf("Azure OpenAI returned an invalid transcript")
	}
	out.Text = result.Text
	return out, nil
}

func transcriptionBody(audio []byte, filename, language string) (bytes.Buffer, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", safeFilename(filename))
	if err != nil {
		return body, "", err
	}
	if _, err := file.Write(audio); err != nil {
		return body, "", err
	}
	// No "model" field: the deployment in the URL already fixes the model.
	if language == "es" || language == "en" {
		if err := writer.WriteField("language", language); err != nil {
			return body, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return body, "", err
	}
	return body, writer.FormDataContentType(), nil
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\\\r\n") {
		return "audio.wav"
	}
	return name
}
