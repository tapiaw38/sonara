// Package localwhisper transcribes through a whisper.cpp server running on this
// machine (whisper-server, POST /inference). Free and offline: audio never
// leaves the computer. It only transcribes; text requests are refused.
package localwhisper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sonara/backend/usage"
	"strings"
	"sync"
	"syscall"
	"time"

	"sonara/backend/internal/domain"
)

var errTextUnsupported = errors.New("Whisper local solo transcribe audio")

const port = "8178"

// idleStop frees the model's VRAM when nothing has been transcribed for a
// while; the next block starts the server again (~1s).
const idleStop = 5 * time.Minute

type Gateway struct {
	baseURL string
	client  *http.Client

	mu       sync.Mutex
	server   *exec.Cmd
	lastUsed time.Time
}

func NewGateway() *Gateway {
	return &Gateway{baseURL: "http://127.0.0.1:" + port, client: &http.Client{Timeout: 60 * time.Second}}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (g *Gateway) ready(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+"/", nil)
	if err != nil {
		return false
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// ensureServer starts whisper-server the first time it is needed, so the model
// only takes VRAM while "local" is actually used. It dies with Sonara (Stop,
// and Pdeathsig if Sonara is killed).
func (g *Gateway) ensureServer(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastUsed = time.Now()
	if g.ready(ctx) {
		return nil
	}
	if g.server != nil && g.server.ProcessState == nil {
		g.server.Process.Kill()
		g.server.Wait()
	}
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".local", "share", "whisper.cpp")
	bin := envOr("WHISPER_SERVER_BIN", filepath.Join(root, "build", "bin", "whisper-server"))
	model := envOr("WHISPER_MODEL", filepath.Join(root, "models", "ggml-large-v3-turbo-q5_0.bin"))
	cmd := exec.Command(bin, "-m", model, "--host", "127.0.0.1", "--port", port, "-t", "4")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("no se pudo iniciar Whisper local (%s): %w", bin, err)
	}
	g.server = cmd
	go cmd.Wait()
	go g.stopWhenIdle(cmd)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if g.ready(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("Whisper local no arrancó a tiempo")
}

func (g *Gateway) stopWhenIdle(cmd *exec.Cmd) {
	for {
		time.Sleep(30 * time.Second)
		g.mu.Lock()
		if g.server != cmd {
			g.mu.Unlock()
			return // stopped or replaced meanwhile
		}
		if time.Since(g.lastUsed) >= idleStop {
			g.mu.Unlock()
			g.Stop()
			return
		}
		g.mu.Unlock()
	}
}

// Stop shuts down the server Sonara started, if any.
func (g *Gateway) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.server != nil && g.server.Process != nil {
		g.server.Process.Signal(syscall.SIGTERM)
		g.server = nil
	}
}

func (g *Gateway) Respond(context.Context, domain.Request, string, string) (string, error) {
	return "", errTextUnsupported
}

func (g *Gateway) RespondStream(context.Context, domain.Request, string, string, func(string)) (string, error) {
	return "", errTextUnsupported
}

func (g *Gateway) Transcribe(ctx context.Context, audio []byte, filename, language string) (_ domain.Transcript, callErr error) {
	if err := g.ensureServer(ctx); err != nil {
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
	if language == "" {
		language = "auto"
	}
	for key, value := range map[string]string{"language": language, "response_format": "json", "temperature": "0"} {
		if err := writer.WriteField(key, value); err != nil {
			return domain.Transcript{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return domain.Transcript{}, err
	}
	out := domain.Transcript{UploadBytes: body.Len()}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/inference", &body)
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	started := time.Now()
	meter := usage.Start(ctx, "local", "whisper-local", usage.Live)
	meter.AudioSeconds = usage.WAVSeconds(audio)
	defer func() { meter.Finish(callErr == nil) }()
	resp, err := g.client.Do(req)
	out.WaitMS = int(time.Since(started).Milliseconds())
	if err != nil {
		out.TotalMS = out.WaitMS
		return out, fmt.Errorf("Whisper local no responde: %w", err)
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	result, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	out.TotalMS = int(time.Since(started).Milliseconds())
	if err != nil {
		return out, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("Whisper local falló con status %d", resp.StatusCode)
	}
	var parsed struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return out, fmt.Errorf("respuesta inválida de Whisper local: %w", err)
	}
	if parsed.Error != "" {
		return out, fmt.Errorf("Whisper local: %s", parsed.Error)
	}
	out.Text = strings.TrimSpace(parsed.Text)
	return out, nil
}
