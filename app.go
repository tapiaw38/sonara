package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// App struct
type App struct {
	ctx       context.Context
	mu        sync.RWMutex
	listening bool
	conversationID string
}

type CaptureStatus struct {
	Path       string `json:"path"`
	CapturedAt string `json:"capturedAt"`
	Queued     bool   `json:"queued"`
	AgentReady bool   `json:"agentReady"`
}

type AnalysisResult struct {
	Response       string `json:"response"`
	ConversationID string `json:"conversationId"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Greet returns a greeting for the given name
func (a *App) StartListening() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listening = true
	return a.listening
}

func (a *App) StopListening() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listening = false
	return a.listening
}

func (a *App) IsListening() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.listening
}

func (a *App) AIEndpoint() string {
	return gillieEndpoint()
}

func gillieEndpoint() string {
	if endpoint := os.Getenv("SONARA_GILLIE_ENDPOINT"); endpoint != "" {
		return strings.TrimRight(endpoint, "/")
	}
	if endpoint := os.Getenv("SONARA_AI_ENDPOINT"); endpoint != "" {
		return strings.TrimRight(endpoint, "/")
	}
	return "http://127.0.0.1:8000"
}

func capturePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "sonara", "captures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "latest.png"), nil
}

// CaptureScreen captures the current Wayland output with grim and queues it for
// the AI adapter. Sending is intentionally deferred until an agent endpoint is configured.
func (a *App) CaptureScreen() (CaptureStatus, error) {
	path, err := capturePath()
	if err != nil {
		return CaptureStatus{}, err
	}
	if output, err := exec.Command("grim", path).CombinedOutput(); err != nil {
		return CaptureStatus{}, fmt.Errorf("grim failed: %w: %s", err, output)
	}
	return a.LastCapture()
}

func (a *App) LastCapture() (CaptureStatus, error) {
	path, err := capturePath()
	if err != nil {
		return CaptureStatus{}, err
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return CaptureStatus{AgentReady: true}, nil
	}
	if err != nil {
		return CaptureStatus{}, err
	}
	return CaptureStatus{
		Path:       path,
		CapturedAt: info.ModTime().Format(time.RFC3339),
		Queued:     true,
		AgentReady: true,
	}, nil
}

func (a *App) ensureGillieConversation() (string, error) {
	a.mu.RLock()
	id := a.conversationID
	a.mu.RUnlock()
	if id != "" {
		return id, nil
	}

	body := strings.NewReader(`{"title":"Sonara"}`)
	request, err := http.NewRequest(http.MethodPost, gillieEndpoint()+"/conversation/", body)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 90 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("Gillie unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("Gillie conversation: %s", strings.TrimSpace(string(message)))
	}
	var payload struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Data.ID == "" {
		return "", fmt.Errorf("Gillie did not return conversation id")
	}
	a.mu.Lock()
	a.conversationID = payload.Data.ID
	a.mu.Unlock()
	return payload.Data.ID, nil
}

// AnalyzeLastCapture sends latest screenshot to Gillie vision and returns its reply.
func (a *App) AnalyzeLastCapture() (AnalysisResult, error) {
	capture, err := a.LastCapture()
	if err != nil {
		return AnalysisResult{}, err
	}
	if capture.Path == "" {
		return AnalysisResult{}, fmt.Errorf("no screenshot available")
	}
	conversationID, err := a.ensureGillieConversation()
	if err != nil {
		return AnalysisResult{}, err
	}
	file, err := os.Open(capture.Path)
	if err != nil {
		return AnalysisResult{}, err
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("content", "Analiza esta captura de pantalla. Ignora por completo cualquier interfaz, ventana, texto, controles, botones o overlay de Sonara que aparezca en la imagen; no los menciones ni los uses como contexto. Analiza únicamente el contenido de la aplicación o llamada que está detrás. Identifica información relevante y responde de forma breve y útil."); err != nil {
		return AnalysisResult{}, err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image_content"; filename="%s"`, filepath.Base(capture.Path)))
	header.Set("Content-Type", "image/png")
	image, err := writer.CreatePart(header)
	if err != nil {
		return AnalysisResult{}, err
	}
	if _, err := io.Copy(image, file); err != nil {
		return AnalysisResult{}, err
	}
	if err := writer.Close(); err != nil {
		return AnalysisResult{}, err
	}

	url := fmt.Sprintf("%s/conversation/%s/message?has_image_processor=activate", gillieEndpoint(), conversationID)
	request, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return AnalysisResult{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := (&http.Client{Timeout: 120 * time.Second}).Do(request)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("Gillie analysis: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return AnalysisResult{}, fmt.Errorf("Gillie analysis: %s", strings.TrimSpace(string(message)))
	}
	var payload struct {
		Data []struct {
			Content string `json:"content"`
			Sender  string `json:"sender"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return AnalysisResult{}, err
	}
	for i := len(payload.Data) - 1; i >= 0; i-- {
		if payload.Data[i].Sender == "assistant" && payload.Data[i].Content != "" {
			return AnalysisResult{Response: payload.Data[i].Content, ConversationID: conversationID}, nil
		}
	}
	return AnalysisResult{}, fmt.Errorf("Gillie returned no assistant response")
}

// SendMessage sends a text message through the same Gillie conversation used by vision.
func (a *App) SendMessage(content string) (AnalysisResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return AnalysisResult{}, fmt.Errorf("message is empty")
	}
	conversationID, err := a.ensureGillieConversation()
	if err != nil {
		return AnalysisResult{}, err
	}

	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/conversation/%s/message", gillieEndpoint(), conversationID),
		strings.NewReader(url.Values{"content": {content}}.Encode()),
	)
	if err != nil {
		return AnalysisResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 120 * time.Second}).Do(request)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("Gillie chat: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return AnalysisResult{}, fmt.Errorf("Gillie chat: %s", strings.TrimSpace(string(message)))
	}

	var payload struct {
		Data []struct {
			Content string `json:"content"`
			Sender  string `json:"sender"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return AnalysisResult{}, err
	}
	for i := len(payload.Data) - 1; i >= 0; i-- {
		if payload.Data[i].Sender == "assistant" && payload.Data[i].Content != "" {
			return AnalysisResult{Response: payload.Data[i].Content, ConversationID: conversationID}, nil
		}
	}
	return AnalysisResult{}, fmt.Errorf("Gillie returned no assistant response")
}
