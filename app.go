package main

import (
	"context"
	"os"
	"sync"
)

// App struct
type App struct {
	ctx       context.Context
	mu        sync.RWMutex
	listening bool
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
