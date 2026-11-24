package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sonara/backend/usage"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

type Gateway interface {
	SetAPIKey(string)
	Respond(context.Context, domain.Request, string, string) (string, error)
	RespondStream(context.Context, domain.Request, string, string, func(string)) (string, error)
	Transcribe(context.Context, []byte, string, string) (domain.Transcript, error)
	Speak(context.Context, string, string, string) ([]byte, error)
}

type gateway struct {
	cfg    config.Config
	client *http.Client
	keyMu  sync.RWMutex
	apiKey string
}

func NewGateway(cfg config.Config) Gateway {
	return &gateway{cfg: cfg, client: &http.Client{Timeout: cfg.RequestTimeout}, apiKey: cfg.OpenAIKey}
}

func (g *gateway) SetAPIKey(key string) {
	g.keyMu.Lock()
	g.apiKey = strings.TrimSpace(key)
	g.keyMu.Unlock()
}

func (g *gateway) configuredKey() string {
	g.keyMu.RLock()
	defer g.keyMu.RUnlock()
	return g.apiKey
}

type responseRequest struct {
	Model           string      `json:"model"`
	Instructions    string      `json:"instructions"`
	Input           []inputItem `json:"input"`
	MaxOutputTokens int         `json:"max_output_tokens"`
	Stream          bool        `json:"stream,omitempty"`
}

type inputItem struct {
	Role    string         `json:"role"`
	Content []inputContent `json:"content"`
}

type inputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responseResult struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// newResponsesRequest builds the shared /responses call. stream flips SSE on;
// everything else is identical, so the two entry points cannot drift apart.
func (g *gateway) newResponsesRequest(ctx context.Context, in domain.Request, model, instructions string, stream bool) (*http.Request, error) {
	apiKey := g.configuredKey()
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI is not configured")
	}
	content := []inputContent{{Type: "input_text", Text: in.Prompt}}
	if in.ImageData != "" {
		content = append(content, inputContent{Type: "input_image", ImageURL: in.ImageData})
	}
	body, err := json.Marshal(responseRequest{
		Model: model, Instructions: instructions,
		Input:           []inputItem{{Role: "user", Content: content}},
		MaxOutputTokens: g.cfg.MaxOutputTokens,
		Stream:          stream,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.OpenAIBaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, nil
}

func (g *gateway) Respond(ctx context.Context, in domain.Request, model, instructions string) (_ string, callErr error) {
	req, err := g.newResponsesRequest(ctx, in, model, instructions, false)
	if err != nil {
		return "", err
	}
	meter := usage.Start(ctx, "openai", model, usage.Copilot)
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	meter.Observe(responseBody)
	var result responseResult
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return "", fmt.Errorf("OpenAI returned an invalid response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if result.Error != nil && result.Error.Message != "" {
			return "", fmt.Errorf("OpenAI request failed: %s", result.Error.Message)
		}
		return "", fmt.Errorf("OpenAI request failed with status %d", resp.StatusCode)
	}
	var text strings.Builder
	for _, item := range result.Output {
		for _, part := range item.Content {
			if part.Type == "output_text" {
				text.WriteString(part.Text)
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("OpenAI returned an empty response")
	}
	return text.String(), nil
}

// streamEvent is the slice of a /responses SSE frame we act on. Every other
// event type (created, in_progress, output_item.*, usage) is ignorable noise.
type streamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Text  string `json:"text"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseStreamEvent decodes one SSE line. Only `data:` carries payload; the
// `event:` line repeats the type and keep-alive comments carry nothing.
func parseStreamEvent(line string) (delta string, done bool, failure string) {
	payload, found := strings.CutPrefix(line, "data:")
	if !found {
		return "", false, ""
	}
	payload = strings.TrimSpace(payload)
	// The Responses API signals completion with a typed event, but tolerate the
	// chat-completions sentinel so a proxy base URL cannot hang the stream.
	if payload == "" || payload == "[DONE]" {
		return "", payload == "[DONE]", ""
	}
	var event streamEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return "", false, ""
	}
	switch event.Type {
	case "response.output_text.delta":
		return event.Delta, false, ""
	case "response.completed":
		return "", true, ""
	case "response.failed", "response.incomplete", "error":
		message := "OpenAI stream failed"
		if event.Error != nil && event.Error.Message != "" {
			message = event.Error.Message
		}
		return "", true, message
	default:
		return "", false, ""
	}
}

// RespondStream streams the answer, handing each token to onDelta as it lands
// and returning the full text. The caller sees the first words in a few hundred
// milliseconds instead of waiting for the whole completion.
func (g *gateway) RespondStream(ctx context.Context, in domain.Request, model, instructions string, onDelta func(string)) (_ string, callErr error) {
	req, err := g.newResponsesRequest(ctx, in, model, instructions, true)
	if err != nil {
		return "", err
	}
	meter := usage.Start(ctx, "openai", model, usage.Copilot)
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var result responseResult
		if json.Unmarshal(errorBody, &result) == nil && result.Error != nil && result.Error.Message != "" {
			return "", fmt.Errorf("OpenAI request failed: %s", result.Error.Message)
		}
		return "", fmt.Errorf("OpenAI request failed with status %d", resp.StatusCode)
	}
	return readResponseStream(io.LimitReader(resp.Body, 8<<20), onDelta, meter)
}

func readResponseStream(body io.Reader, onDelta func(string), meters ...*usage.Meter) (string, error) {
	var text strings.Builder
	completed := false
	scanner := bufio.NewScanner(body)
	// A single SSE frame can carry a long reasoning payload, so the default
	// 64KB token limit is not enough.
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		usage.ObserveSSE(scanner.Bytes(), meters...)
		delta, done, failure := parseStreamEvent(scanner.Text())
		if failure != "" {
			return "", fmt.Errorf("OpenAI request failed: %s", failure)
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
		return text.String(), fmt.Errorf("OpenAI stream ended before response.completed")
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("OpenAI returned an empty response")
	}
	return text.String(), nil
}

// Transcribe returns the transcript along with what it took to get it. The
// timings are split because a single total cannot tell a slow upload apart from
// a queue wait on OpenAI's side, and those have opposite fixes.
func (g *gateway) Transcribe(ctx context.Context, audio []byte, filename, language string) (domain.Transcript, error) {
	return g.TranscribeWithPrompt(ctx, audio, filename, language, "")
}

func (g *gateway) TranscribeWithPrompt(ctx context.Context, audio []byte, filename, language, prompt string) (_ domain.Transcript, callErr error) {
	apiKey := g.configuredKey()
	if apiKey == "" {
		return domain.Transcript{}, fmt.Errorf("OpenAI is not configured")
	}
	body, contentType, err := transcriptionBody(audio, filename, language, g.cfg.TranscribeModel, prompt)
	if err != nil {
		return domain.Transcript{}, err
	}
	out := domain.Transcript{UploadBytes: body.Len()}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.OpenAIBaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	started := time.Now()
	meter := usage.Start(ctx, "openai", g.cfg.TranscribeModel, usage.Live)
	meter.AudioSeconds = usage.WAVSeconds(audio)
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	// Do returns once the response headers are in, so this split separates
	// "waiting for OpenAI" from "downloading the transcript".
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
		return out, fmt.Errorf("OpenAI transcription failed with status %d", resp.StatusCode)
	}
	meter.Observe(resultBody)
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resultBody, &result); err != nil {
		return out, fmt.Errorf("OpenAI returned an invalid transcript")
	}
	out.Text = result.Text
	return out, nil
}

// Speak turns text into spoken audio (wav) using OpenAI's TTS endpoint.
func (g *gateway) Speak(ctx context.Context, text, voice, model string) (_ []byte, callErr error) {
	apiKey := g.configuredKey()
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI is not configured")
	}
	instructions := ""
	if strings.HasPrefix(model, "gpt-4o-mini-tts") {
		instructions = "Sound like a person having a one-on-one conversation. Speak with warm, understated curiosity, natural contractions where written, and subtle changes in pitch and pace. Pause briefly between thoughts, link words naturally, and make the final question sound genuinely inquisitive. Use the language, dialect, and exact words of the input."
	}
	body, err := json.Marshal(struct {
		Model          string `json:"model"`
		Voice          string `json:"voice"`
		Input          string `json:"input"`
		ResponseFormat string `json:"response_format"`
		Instructions   string `json:"instructions,omitempty"`
	}{Model: model, Voice: voice, Input: text, ResponseFormat: "wav", Instructions: instructions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.OpenAIBaseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	meter := usage.Start(ctx, "openai", model, usage.TTS)
	meter.Characters = int64(utf8.RuneCountInString(text))
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var result responseResult
		if json.Unmarshal(errorBody, &result) == nil && result.Error != nil && result.Error.Message != "" {
			return nil, fmt.Errorf("OpenAI TTS request failed: %s", result.Error.Message)
		}
		return nil, fmt.Errorf("OpenAI TTS request failed with status %d", resp.StatusCode)
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("OpenAI TTS returned no audio")
	}
	meter.AudioSeconds = usage.WAVSeconds(audio)
	return audio, nil
}

func transcriptionBody(audio []byte, filename, language, model, prompt string) (bytes.Buffer, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", safeFilename(filename))
	if err != nil {
		return body, "", err
	}
	if _, err := file.Write(audio); err != nil {
		return body, "", err
	}
	if err := writer.WriteField("model", model); err != nil {
		return body, "", err
	}
	if language == "es" || language == "en" {
		if err := writer.WriteField("language", language); err != nil {
			return body, "", err
		}
	}
	if prompt != "" && !strings.HasPrefix(model, "gpt-4o-transcribe-diarize") {
		if err := writer.WriteField("prompt", prompt); err != nil {
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
