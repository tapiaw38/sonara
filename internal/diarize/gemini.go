package diarize

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sonara/backend/usage"
	"strconv"
	"strings"
	"sync"
	"time"

	"sonara/internal/audio"
	"sonara/internal/logx"
)

// GeminiBaseURL is the origin shared by the Interactions and Files APIs.
const GeminiBaseURL = "https://generativelanguage.googleapis.com"

const geminiWAV = "audio/wav"

type geminiFile struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	State    string `json:"state"`
}

// Gemini diarizes and detects echo through the Files and Interactions APIs.
type Gemini struct {
	client  *http.Client
	baseURL string
	keyMu   sync.RWMutex
	apiKey  string
}

// NewGemini builds the client; a nil client gets one bounded by FinalTimeout.
// The base URL is injectable so tests can use a fake server.
func NewGemini(client *http.Client, baseURL string) *Gemini {
	if client == nil {
		client = &http.Client{Timeout: FinalTimeout}
	}
	return &Gemini{client: client, baseURL: baseURL}
}

func (g *Gemini) SetAPIKey(key string) {
	g.keyMu.Lock()
	g.apiKey = strings.TrimSpace(key)
	g.keyMu.Unlock()
}

func (g *Gemini) key() string {
	g.keyMu.RLock()
	defer g.keyMu.RUnlock()
	return g.apiKey
}

func geminiModelName() string {
	if v := strings.TrimSpace(os.Getenv("GEMINI_MODEL")); v != "" {
		return v
	}
	return "gemini-4-hydrogen"
}

// diarizePrompt asks for speaker turns only, not a transcript: Sonara already
// has the words from its own ASR pipeline and only needs who said them.
const diarizePrompt = "This is the audio captured from one side of a meeting (everyone except the listener). " +
	"Identify who is speaking and when. Return ONLY a JSON array of speaker turns: each item has " +
	"start_ms and end_ms (integer milliseconds from the very start of this audio) and speaker " +
	"(a short label such as \"Speaker 1\", kept the same every time that voice speaks again). " +
	"Merge consecutive turns from the same speaker. Do not transcribe any words. Ignore silence."

var diarizeSchema = map[string]any{
	"type": "array",
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"start_ms": map[string]any{"type": "integer"},
			"end_ms":   map[string]any{"type": "integer"},
			"speaker":  map[string]any{"type": "string"},
		},
		"required": []string{"start_ms", "end_ms", "speaker"},
	},
}

// Diarize sends one WAV file (the whole meeting_output track) and returns its
// speaker spans, each independently labeled by the model: stabilizing those
// labels across a session is StableKeys's job, not this call's.
func (g *Gemini) Diarize(ctx context.Context, wav []byte, _ int) ([]Span, error) {
	apiKey := g.key()
	if apiKey == "" {
		return nil, fmt.Errorf("falta la clave de Gemini (Configuración)")
	}
	fileURI, fileName, mimeType, err := g.uploadFile(ctx, apiKey, wav)
	if err != nil {
		return nil, fmt.Errorf("no se pudo subir el audio a Gemini: %w", err)
	}
	// Best-effort cleanup on an independent context: a canceled caller must
	// not skip deleting what was already uploaded. Google expires it by
	// itself after 48h either way.
	defer g.cleanup(ctx, fileName, apiKey)
	return g.requestDiarization(ctx, apiKey, fileURI, mimeType, wavDurationMS(wav))
}

func (g *Gemini) cleanup(parent context.Context, fileName, apiKey string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	if err := g.deleteFile(ctx, apiKey, fileName); err != nil {
		logx.Logf("could not delete the meeting audio uploaded to Gemini: %v", err)
	}
}

// uploadFile runs the Files API's resumable upload in two requests: one to
// start the session and get an upload URL, one to send the bytes.
func (g *Gemini) uploadFile(ctx context.Context, apiKey string, wav []byte) (uri, name, mimeType string, err error) {
	mimeType = geminiWAV
	start, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/upload/v1beta/files",
		strings.NewReader(`{"file":{"display_name":"sonara-meeting"}}`))
	if err != nil {
		return "", "", "", err
	}
	start.Header.Set("x-goog-api-key", apiKey)
	start.Header.Set("Content-Type", "application/json")
	start.Header.Set("X-Goog-Upload-Protocol", "resumable")
	start.Header.Set("X-Goog-Upload-Command", "start")
	start.Header.Set("X-Goog-Upload-Header-Content-Length", strconv.Itoa(len(wav)))
	start.Header.Set("X-Goog-Upload-Header-Content-Type", mimeType)
	startResp, err := g.filesDo(start)
	if err != nil {
		return "", "", "", err
	}
	uploadURL := startResp.Header.Get("x-goog-upload-url")
	io.Copy(io.Discard, io.LimitReader(startResp.Body, 1<<16))
	startResp.Body.Close()
	if startResp.StatusCode < 200 || startResp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("estado %d al iniciar la subida", startResp.StatusCode)
	}
	if uploadURL == "" {
		return "", "", "", fmt.Errorf("la respuesta no incluyó la URL de subida")
	}

	put, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(wav))
	if err != nil {
		return "", "", "", err
	}
	put.Header.Set("Content-Length", strconv.Itoa(len(wav)))
	put.Header.Set("X-Goog-Upload-Offset", "0")
	put.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	putResp, err := g.filesDo(put)
	if err != nil {
		return "", "", "", err
	}
	defer putResp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(putResp.Body, 1<<16))
	if err != nil {
		return "", "", "", err
	}
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("estado %d al subir el audio", putResp.StatusCode)
	}
	var decoded struct {
		File geminiFile `json:"file"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.File.Name == "" {
		return "", "", "", fmt.Errorf("la subida no devolvió el nombre del archivo")
	}
	file := decoded.File
	for file.State == "PROCESSING" {
		select {
		case <-ctx.Done():
			g.cleanup(ctx, decoded.File.Name, apiKey)
			return "", "", "", ctx.Err()
		case <-time.After(time.Second):
		}
		file, err = g.getFile(ctx, apiKey, decoded.File.Name)
		if err != nil {
			g.cleanup(ctx, decoded.File.Name, apiKey)
			return "", "", "", err
		}
	}
	if file.State != "ACTIVE" || file.URI == "" || file.MimeType == "" {
		g.cleanup(ctx, decoded.File.Name, apiKey)
		return "", "", "", fmt.Errorf("Gemini no devolvió un archivo de audio usable (estado %q)", file.State)
	}
	return file.URI, decoded.File.Name, file.MimeType, nil
}

func (g *Gemini) getFile(ctx context.Context, apiKey, fileName string) (geminiFile, error) {
	var file geminiFile
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+"/v1beta/"+fileName, nil)
	if err != nil {
		return file, err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	resp, err := g.filesDo(req)
	if err != nil {
		return file, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return file, fmt.Errorf("estado %d al consultar el archivo de Gemini", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&file)
	return file, err
}

// deleteFile removes the uploaded resource through the Files API.
func (g *Gemini) deleteFile(ctx context.Context, apiKey, fileName string) error {
	if fileName == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, g.baseURL+"/v1beta/"+fileName, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	resp, err := g.filesDo(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("estado %d", resp.StatusCode)
	}
	return nil
}

func (g *Gemini) filesDo(req *http.Request) (*http.Response, error) {
	m := usage.Start(req.Context(), "gemini", "gemini-files", usage.Diarization)
	resp, err := g.client.Do(req)
	m.Finish(err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300)
	return resp, err
}

type geminiInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// URI is for Files-API-uploaded audio; Data is inline base64 bytes (small
	// clips, like a voice sample, can skip the upload round trip entirely).
	URI      string `json:"uri,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type geminiResponseFormat struct {
	Type     string         `json:"type"`
	MimeType string         `json:"mime_type"`
	Schema   map[string]any `json:"schema"`
}

type geminiRequest struct {
	Model          string               `json:"model"`
	Input          []geminiInput        `json:"input"`
	ResponseFormat geminiResponseFormat `json:"response_format"`
}

type geminiInteraction struct {
	Status string `json:"status"`
	Steps  []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"steps"`
}

// audio.WAV writes a 44-byte header followed by mono 16-bit PCM.
func wavDurationMS(wav []byte) int64 {
	return int64(max(0, len(wav)-44)) / (audio.SampleRate * 2 / 1000)
}

func (g *Gemini) requestDiarization(ctx context.Context, apiKey, fileURI, mimeType string, durationMS int64) ([]Span, error) {
	text, err := g.interact(ctx, apiKey, geminiRequest{
		Model: geminiModelName(),
		Input: []geminiInput{
			{Type: "text", Text: diarizePrompt},
			{Type: "audio", URI: fileURI, MimeType: mimeType},
		},
		ResponseFormat: geminiResponseFormat{Type: "text", MimeType: "application/json", Schema: diarizeSchema},
	}, float64(durationMS)/1000)
	if err != nil {
		return nil, err
	}
	var spans []Span
	if err := json.Unmarshal([]byte(text), &spans); err != nil || spans == nil {
		return nil, fmt.Errorf("Gemini no devolvió la lista de hablantes esperada")
	}
	return cleanSpans(spans, durationMS), nil
}

// interact is the shared POST /interactions call: build the request, check
// the HTTP and interaction status, and concatenate the model's text output.
// Callers each decode that text into their own expected JSON shape.
func (g *Gemini) interact(ctx context.Context, apiKey string, request geminiRequest, seconds float64) (_ string, callErr error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/v1/interactions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	meter := usage.Start(ctx, "gemini", request.Model, usage.Diarization)
	meter.AudioSeconds = seconds
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Gemini respondió con estado %d", resp.StatusCode)
	}
	meter.Observe(respBody)
	var decoded geminiInteraction
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return "", fmt.Errorf("Gemini devolvió una respuesta inválida")
	}
	if decoded.Status != "completed" {
		return "", fmt.Errorf("Gemini no completó la solicitud (estado %q)", decoded.Status)
	}
	var text strings.Builder
	for _, step := range decoded.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, part := range step.Content {
			if part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("Gemini no devolvió resultado")
	}
	return text.String(), nil
}

// EchoSpan is a stretch of the meeting_output track where the reference
// voice was heard, in the same absolute clock as transcript_segments.
type EchoSpan struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

var echoSchema = map[string]any{
	"type": "array",
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"start_ms": map[string]any{"type": "integer"},
			"end_ms":   map[string]any{"type": "integer"},
		},
		"required": []string{"start_ms", "end_ms"},
	},
}

const echoPrompt = "The first audio is a short voice sample of one specific person. The second audio is a " +
	"separate recording: the other side of a meeting (everyone except that person). Determine whether " +
	"that same person's voice can be heard anywhere in the second audio — for example, played back as an " +
	"echo through someone else's speakers, not someone else who merely sounds similar. Return ONLY a JSON " +
	"array of time ranges, each with start_ms and end_ms (integer milliseconds from the start of the " +
	"SECOND audio), covering every moment that voice is heard there. If it is never heard, return []."

// DetectEcho uploads the meeting_output recording (sampleWav stays inline:
// a voice sample is a few hundred KB at most, well under any inline limit)
// and asks whether the reference voice appears in it.
func (g *Gemini) DetectEcho(ctx context.Context, sampleWav, meetingWav []byte) ([]EchoSpan, error) {
	apiKey := g.key()
	if apiKey == "" {
		return nil, fmt.Errorf("falta la clave de Gemini (Configuración)")
	}
	fileURI, fileName, mimeType, err := g.uploadFile(ctx, apiKey, meetingWav)
	if err != nil {
		return nil, fmt.Errorf("no se pudo subir el audio de la reunión: %w", err)
	}
	defer g.cleanup(ctx, fileName, apiKey)
	text, err := g.interact(ctx, apiKey, geminiRequest{
		Model: geminiModelName(),
		Input: []geminiInput{
			{Type: "text", Text: echoPrompt},
			{Type: "audio", Data: base64.StdEncoding.EncodeToString(sampleWav), MimeType: geminiWAV},
			{Type: "audio", URI: fileURI, MimeType: mimeType},
		},
		ResponseFormat: geminiResponseFormat{Type: "text", MimeType: "application/json", Schema: echoSchema},
	}, usage.WAVSeconds(sampleWav)+usage.WAVSeconds(meetingWav))
	if err != nil {
		return nil, err
	}
	var spans []EchoSpan
	if err := json.Unmarshal([]byte(text), &spans); err != nil || spans == nil {
		return nil, fmt.Errorf("Gemini no devolvió la lista de intervalos esperada")
	}
	return cleanEchoSpans(spans, wavDurationMS(meetingWav)), nil
}
