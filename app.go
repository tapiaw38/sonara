package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// App struct
type App struct {
	ctx                 context.Context
	mu                  sync.RWMutex
	listening           bool
	conversationID      string
	latestVisualContext string
	listenerCancel      context.CancelFunc
	transcript          string
	listenStatus        string
	voiceReply          string
	voiceSequence       uint64
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

type ListeningState struct {
	Listening  bool   `json:"listening"`
	Transcript string `json:"transcript"`
	Status     string `json:"status"`
	Response   string `json:"response"`
	Sequence   uint64 `json:"sequence"`
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
	if a.listening {
		a.mu.Unlock()
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.listening = true
	a.listenerCancel = cancel
	a.listenStatus = "Conectando audio de Meet…"
	a.transcript = "Esperando intervención en Meet…"
	a.mu.Unlock()
	go a.listenMeetAudio(ctx)
	return true
}

func (a *App) StopListening() bool {
	a.mu.Lock()
	if a.listenerCancel != nil {
		a.listenerCancel()
		a.listenerCancel = nil
	}
	a.listening = false
	a.listenStatus = "Escucha pausada"
	a.mu.Unlock()
	return false
}

func (a *App) ListeningStatus() ListeningState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return ListeningState{
		Listening: a.listening, Transcript: a.transcript, Status: a.listenStatus,
		Response: a.voiceReply, Sequence: a.voiceSequence,
	}
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
			responseText := payload.Data[i].Content
			a.mu.Lock()
			a.latestVisualContext = truncateContext(responseText, 6000)
			a.mu.Unlock()
			return AnalysisResult{Response: responseText, ConversationID: conversationID}, nil
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

	a.mu.RLock()
	visualContext := a.latestVisualContext
	a.mu.RUnlock()
	form := url.Values{"content": {content}}
	if visualContext != "" {
		form.Set("context", "Latest screenshot analysis from this Sonara session. Use it to answer follow-up questions directly when relevant:\n"+visualContext)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/conversation/%s/message", gillieEndpoint(), conversationID),
		strings.NewReader(form.Encode()),
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

func truncateContext(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[truncated]"
}

const (
	meetSampleRate     = 16000
	meetFrameBytes     = 3200 // 100ms mono s16 PCM
	meetSilenceFrames  = 10
	meetMinimumFrames  = 6
	meetVoiceThreshold = 0.012
)

// listenMeetAudio captures only Chrome/Chromium PipeWire output. It never falls
// back to the default system monitor, so unrelated desktop audio is excluded.
func (a *App) listenMeetAudio(ctx context.Context) {
	serial, err := chromeAudioSerial(ctx)
	if err != nil {
		a.setListeningFailure("No se detectó audio de Chrome/Meet. Abre la llamada y reproduce audio.")
		return
	}
	command := exec.CommandContext(ctx, "pw-record", "--target", serial, "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	stdout, err := command.StdoutPipe()
	if err != nil || command.Start() != nil {
		a.setListeningFailure("No se pudo conectar a audio de Chrome/Meet")
		return
	}
	defer command.Wait()
	a.setListenStatus("Escuchando audio de Meet")

	frame := make([]byte, meetFrameBytes)
	segment := make([]byte, 0, meetSampleRate*2*12)
	recording, voiceFrames, silenceFrames := false, 0, 0
	for {
		if _, err := io.ReadFull(stdout, frame); err != nil {
			if ctx.Err() == nil {
				a.setListeningFailure("Audio de Chrome/Meet finalizó")
			}
			return
		}
		voice := pcmRMS(frame) >= meetVoiceThreshold
		if !recording {
			if voice {
				voiceFrames++
				segment = append(segment, frame...)
				if voiceFrames >= 3 {
					recording, silenceFrames = true, 0
					a.setListenStatus("Intervención detectada…")
				}
			} else {
				voiceFrames, segment = 0, segment[:0]
			}
			continue
		}
		segment = append(segment, frame...)
		if voice {
			silenceFrames = 0
		} else {
			silenceFrames++
		}
		if silenceFrames < meetSilenceFrames {
			continue
		}
		if len(segment)/meetFrameBytes >= meetMinimumFrames {
			a.processMeetSegment(segment)
		}
		recording, voiceFrames, silenceFrames = false, 0, 0
		segment = segment[:0]
		if ctx.Err() != nil {
			return
		}
		a.setListenStatus("Escuchando audio de Meet")
	}
}

func chromeAudioSerial(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return "", err
	}
	var nodes []struct {
		Type string `json:"type"`
		ID   any    `json:"id"`
		Info struct {
			State string         `json:"state"`
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(output, &nodes); err != nil {
		return "", err
	}
	for _, node := range nodes {
		if node.Type != "PipeWire:Interface:Node" || node.Info.Props["media.class"] != "Stream/Output/Audio" {
			continue
		}
		identity := strings.ToLower(fmt.Sprintf("%v %v %v", node.Info.Props["application.name"], node.Info.Props["application.process.binary"], node.Info.Props["node.name"]))
		if !strings.Contains(identity, "chrome") && !strings.Contains(identity, "chromium") && !strings.Contains(identity, "google-chrome") {
			continue
		}
		if serial, ok := node.Info.Props["object.serial"]; ok {
			return fmt.Sprint(serial), nil
		}
		return fmt.Sprint(node.ID), nil
	}
	return "", fmt.Errorf("Chrome no tiene stream de salida")
}

func pcmRMS(frame []byte) float64 {
	var sum float64
	for i := 0; i+1 < len(frame); i += 2 {
		sample := float64(int16(binary.LittleEndian.Uint16(frame[i:i+2]))) / 32768
		sum += sample * sample
	}
	return math.Sqrt(sum / float64(len(frame)/2))
}

func (a *App) processMeetSegment(pcm []byte) {
	a.setListenStatus("Gillie transcribiendo…")
	result, transcript, err := a.sendMeetAudio(wavFromPCM(pcm))
	if err != nil {
		a.setListenStatus("Error de transcripción: " + err.Error())
		return
	}
	a.mu.Lock()
	a.transcript, a.voiceReply = transcript, result.Response
	a.voiceSequence++
	a.listenStatus = "Respuesta sugerida lista"
	a.mu.Unlock()
}

func (a *App) sendMeetAudio(audio []byte) (AnalysisResult, string, error) {
	conversationID, err := a.ensureGillieConversation()
	if err != nil {
		return AnalysisResult{}, "", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("content", "Audio de Google Meet. Transcribe la intervención y responde breve, directo y útil."); err != nil {
		return AnalysisResult{}, "", err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="voice_content"; filename="meet-segment.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(header)
	if err != nil {
		return AnalysisResult{}, "", err
	}
	if _, err := part.Write(audio); err != nil {
		return AnalysisResult{}, "", err
	}
	if err := writer.Close(); err != nil {
		return AnalysisResult{}, "", err
	}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/conversation/%s/message", gillieEndpoint(), conversationID), &body)
	if err != nil {
		return AnalysisResult{}, "", err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := (&http.Client{Timeout: 120 * time.Second}).Do(request)
	if err != nil {
		return AnalysisResult{}, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AnalysisResult{}, "", fmt.Errorf("Gillie audio respondió %s", response.Status)
	}
	var payload struct {
		Data []struct {
			Content string `json:"content"`
			Sender  string `json:"sender"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return AnalysisResult{}, "", err
	}
	transcript := "Intervención detectada."
	for _, message := range payload.Data {
		if message.Sender == "user" && strings.Contains(message.Content, "[Contexto de voz]:") {
			transcript = strings.TrimSpace(strings.SplitN(message.Content, "[Contexto de voz]:", 2)[1])
		}
	}
	for i := len(payload.Data) - 1; i >= 0; i-- {
		if payload.Data[i].Sender == "assistant" && payload.Data[i].Content != "" {
			return AnalysisResult{Response: payload.Data[i].Content, ConversationID: conversationID}, transcript, nil
		}
	}
	return AnalysisResult{}, "", fmt.Errorf("Gillie returned no assistant response")
}

func wavFromPCM(pcm []byte) []byte {
	dataSize := uint32(len(pcm))
	wav := make([]byte, 44+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], 36+dataSize)
	copy(wav[8:12], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], meetSampleRate)
	binary.LittleEndian.PutUint32(wav[28:32], meetSampleRate*2)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], dataSize)
	copy(wav[44:], pcm)
	return wav
}

func (a *App) setListenStatus(status string) { a.mu.Lock(); a.listenStatus = status; a.mu.Unlock() }
func (a *App) setListeningFailure(status string) {
	a.mu.Lock()
	a.listening = false
	a.listenerCancel = nil
	a.listenStatus = status
	a.transcript = status
	a.mu.Unlock()
}
