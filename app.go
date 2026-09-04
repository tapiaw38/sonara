package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// App struct
type App struct {
	ctx       context.Context
	mu        sync.RWMutex
	listening bool
}

type CaptureStatus struct {
	Path       string `json:"path"`
	CapturedAt string `json:"capturedAt"`
	Queued     bool   `json:"queued"`
	AgentReady bool   `json:"agentReady"`
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
	if endpoint := os.Getenv("SONARA_AI_ENDPOINT"); endpoint != "" {
		return endpoint
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
		return CaptureStatus{AgentReady: os.Getenv("SONARA_AI_ENDPOINT") != ""}, nil
	}
	if err != nil {
		return CaptureStatus{}, err
	}
	return CaptureStatus{
		Path:       path,
		CapturedAt: info.ModTime().Format(time.RFC3339),
		Queued:     true,
		AgentReady: os.Getenv("SONARA_AI_ENDPOINT") != "",
	}, nil
}
