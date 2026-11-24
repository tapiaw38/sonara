// Package deepseek is the cheaper text and speech provider. It never sees
// images: DeepSeek's chat endpoint is text-only here, so vision stays on OpenAI.
package deepseek

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sonara/backend/usage"
	"strconv"
	"strings"
	"sync"
	"time"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

// Limits of POST /v1/tools/asr. Checked before upload so an oversized block
// fails locally and instantly instead of after a wasted upload.
const (
	maxASRBytes    = 8 << 20
	maxASRDuration = 120 * time.Second
	// 16 kHz mono s16 PCM, plus the 44-byte WAV header.
	asrBytesPerSecond = 16000 * 2
	wavHeaderBytes    = 44
)

// ErrNotConfigured lets the router tell "no key" apart from a provider failure.
var ErrNotConfigured = errors.New("DeepSeek is not configured")

type Gateway struct {
	cfg    config.Config
	client *http.Client
	keyMu  sync.RWMutex
	apiKey string
}

func NewGateway(cfg config.Config) *Gateway {
	return &Gateway{cfg: cfg, client: &http.Client{Timeout: cfg.RequestTimeout}, apiKey: cfg.DeepSeekKey}
}

func (g *Gateway) SetAPIKey(key string) {
	g.keyMu.Lock()
	g.apiKey = strings.TrimSpace(key)
	g.keyMu.Unlock()
}

func (g *Gateway) Configured() bool { return g.key() != "" }

func (g *Gateway) key() string {
	g.keyMu.RLock()
	defer g.keyMu.RUnlock()
	return g.apiKey
}

// modelFor maps the tier the use case picked (it hands over an OpenAI model
// name) onto the DeepSeek model of the same tier.
func (g *Gateway) modelFor(model string) string {
	if model == g.cfg.ReasoningModel || model == g.cfg.ExpertModel {
		return g.cfg.DeepSeekModelThink
	}
	return g.cfg.DeepSeekModelFast
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	StreamOptions map[string]bool `json:"stream_options,omitempty"`
	Model         string          `json:"model"`
	Messages      []chatMessage   `json:"messages"`
	MaxTokens     int             `json:"max_tokens"`
	Stream        bool            `json:"stream,omitempty"`
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

func (g *Gateway) newChatRequest(ctx context.Context, in domain.Request, model, instructions string, stream bool) (*http.Request, error) {
	apiKey := g.key()
	if apiKey == "" {
		return nil, ErrNotConfigured
	}
	if in.ImageData != "" {
		return nil, errors.New("DeepSeek does not accept images")
	}
	payload := chatRequest{
		Model: g.modelFor(model),
		Messages: []chatMessage{
			{Role: "system", Content: instructions},
			{Role: "user", Content: in.Prompt},
		},
		MaxTokens: g.cfg.MaxOutputTokens,
		Stream:    stream,
	}
	if stream {
		payload.StreamOptions = map[string]bool{"include_usage": true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.DeepSeekBaseURL+"/chat/completions", bytes.NewReader(body))
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

// failure turns a non-2xx body into an error without echoing the prompt.
func failure(status int, body []byte) error {
	var result chatResult
	if json.Unmarshal(body, &result) == nil && result.Error != nil && result.Error.Message != "" {
		return fmt.Errorf("DeepSeek request failed (%d): %s", status, result.Error.Message)
	}
	return fmt.Errorf("DeepSeek request failed with status %d", status)
}

func (g *Gateway) Respond(ctx context.Context, in domain.Request, model, instructions string) (_ string, callErr error) {
	req, err := g.newChatRequest(ctx, in, model, instructions, false)
	if err != nil {
		return "", err
	}
	meter := usage.Start(ctx, "deepseek", g.modelFor(model), usage.Copilot)
	defer func() { meter.Finish(callErr == nil) }()
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
	meter.Observe(body)
	var result chatResult
	if err := json.Unmarshal(body, &result); err != nil {
		return "", errors.New("DeepSeek returned an invalid response")
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", errors.New("DeepSeek returned an empty response")
	}
	return result.Choices[0].Message.Content, nil
}

// parseStreamLine decodes one chat-completions SSE line. Only `data:` carries
// payload and `[DONE]` closes the stream.
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
			message = "DeepSeek stream failed"
		}
		return "", true, message
	}
	if len(result.Choices) == 0 {
		return "", false, ""
	}
	return result.Choices[0].Delta.Content, false, ""
}

func (g *Gateway) RespondStream(ctx context.Context, in domain.Request, model, instructions string, onDelta func(string)) (_ string, callErr error) {
	req, err := g.newChatRequest(ctx, in, model, instructions, true)
	if err != nil {
		return "", err
	}
	meter := usage.Start(ctx, "deepseek", g.modelFor(model), usage.Copilot)
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return "", failure(resp.StatusCode, body)
	}
	return readStream(io.LimitReader(resp.Body, 8<<20), onDelta, meter)
}

func readStream(body io.Reader, onDelta func(string), meters ...*usage.Meter) (string, error) {
	var text strings.Builder
	completed := false
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		usage.ObserveSSE(scanner.Bytes(), meters...)
		delta, done, failed := parseStreamLine(scanner.Text())
		if failed != "" {
			return text.String(), fmt.Errorf("DeepSeek request failed: %s", failed)
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
		return text.String(), errors.New("DeepSeek stream ended before [DONE]")
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("DeepSeek returned an empty response")
	}
	return text.String(), nil
}

// checkASRAudio rejects what the endpoint would refuse. The size check is
// exact; the duration check assumes the 16 kHz mono PCM the app records.
func checkASRAudio(audio []byte) error {
	if len(audio) > maxASRBytes {
		return fmt.Errorf("audio is %d bytes; DeepSeek accepts at most %d", len(audio), maxASRBytes)
	}
	seconds := time.Duration(max(len(audio)-wavHeaderBytes, 0)) * time.Second / asrBytesPerSecond
	if seconds > maxASRDuration {
		return fmt.Errorf("audio is about %ds; DeepSeek accepts at most %ds", int(seconds.Seconds()), int(maxASRDuration.Seconds()))
	}
	return nil
}

// Transcribe sends one block to /v1/tools/asr. DeepSeek has no language
// parameter (it detects the language itself), so language is ignored here.
func (g *Gateway) Transcribe(ctx context.Context, audio []byte, filename, _ string) (_ domain.Transcript, callErr error) {
	apiKey := g.key()
	if apiKey == "" {
		return domain.Transcript{}, ErrNotConfigured
	}
	if err := checkASRAudio(audio); err != nil {
		return domain.Transcript{}, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if strings.TrimSpace(filename) == "" || strings.ContainsAny(filename, "/\\\r\n") {
		filename = "audio.wav"
	}
	file, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return domain.Transcript{}, err
	}
	if _, err := file.Write(audio); err != nil {
		return domain.Transcript{}, err
	}
	if err := writer.Close(); err != nil {
		return domain.Transcript{}, err
	}
	out := domain.Transcript{UploadBytes: body.Len()}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.DeepSeekBaseURL+"/v1/tools/asr", &body)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	started := time.Now()
	meter := usage.Start(ctx, "deepseek", "deepseek-asr", usage.Live)
	meter.AudioSeconds = usage.WAVSeconds(audio)
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	out.WaitMS = int(time.Since(started).Milliseconds())
	if err != nil {
		out.TotalMS = out.WaitMS
		return out, err
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	out.RequestID = resp.Header.Get("x-request-id")
	if ms, err := strconv.Atoi(resp.Header.Get("x-processing-ms")); err == nil {
		out.ProcessingMS = ms
	}
	result, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	out.TotalMS = int(time.Since(started).Milliseconds())
	if err != nil {
		return out, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("DeepSeek transcription failed with status %d", resp.StatusCode)
	}
	meter.Observe(result)
	var decoded struct {
		RequestID string `json:"request_id"`
		Text      string `json:"text"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return out, errors.New("DeepSeek returned an invalid transcript")
	}
	if out.RequestID == "" {
		out.RequestID = decoded.RequestID
	}
	out.Text = decoded.Text
	return out, nil
}
