package main

import (
	"bufio"
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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	streaming           bool
	manualSend          bool
	pendingAudio        []byte
	audioLevel          float64
	audioLanguage       string
	translation         string
	floatedForOverlay   bool
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
	Listening      bool    `json:"listening"`
	Transcript     string  `json:"transcript"`
	Status         string  `json:"status"`
	Response       string  `json:"response"`
	Sequence       uint64  `json:"sequence"`
	ManualSend     bool    `json:"manualSend"`
	PendingSeconds float64 `json:"pendingSeconds"`
	Streaming      bool    `json:"streaming"`
	Language       string  `json:"language"`
	Translation    string  `json:"translation"`
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
	a.audioLevel = 0
	a.mu.Unlock()
	return false
}

func (a *App) ListeningStatus() ListeningState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return ListeningState{
		Listening: a.listening, Transcript: a.transcript, Status: a.listenStatus,
		Response: a.voiceReply, Sequence: a.voiceSequence,
		ManualSend:     a.manualSend,
		PendingSeconds: float64(len(a.pendingAudio)) / float64(meetSampleRate*2),
		Streaming:      a.streaming,
		Language:       a.audioLanguage,
		Translation:    a.translation,
	}
}

// SetManualSend switches between sending every detected segment automatically
// and holding the last one until the user presses send.
func (a *App) SetManualSend(manual bool) bool {
	a.mu.Lock()
	a.manualSend = manual
	if manual {
		a.listenStatus = "Modo manual: los fragmentos quedan en espera"
	} else {
		a.listenStatus = "Modo automático: se envía al detectar silencio"
		a.pendingAudio = nil
	}
	a.mu.Unlock()
	return manual
}

// SendPendingSegment sends the segment held in manual mode.
func (a *App) SendPendingSegment() bool {
	a.mu.Lock()
	pending := a.pendingAudio
	a.pendingAudio = nil
	a.mu.Unlock()
	if len(pending) == 0 {
		return false
	}
	go a.deliverMeetSegment(pending)
	return true
}

func (a *App) DiscardPendingSegment() bool {
	a.mu.Lock()
	a.pendingAudio = nil
	a.listenStatus = "Fragmento descartado"
	a.mu.Unlock()
	return true
}

// SetAudioLanguage records the language spoken in the call. Replies are always
// in Spanish; this only says what Gillie is listening to.
func (a *App) SetAudioLanguage(language string) string {
	if language != "en" {
		language = "es"
	}
	a.mu.Lock()
	a.audioLanguage = language
	a.mu.Unlock()
	return language
}

func (a *App) audioLanguageOrDefault() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.audioLanguage == "" {
		return "es"
	}
	return a.audioLanguage
}

const (
	// translationMarker opens the line carrying what the other person said,
	// translated; it is lifted out of the reply and shown under the transcript.
	translationMarker = "TRADUCCIÓN:"
	// replyTranslationMarker opens the Spanish version of the suggested answer,
	// which stays in the reply panel right below the English wording.
	replyTranslationMarker = "ES:"
)

// suggestionBrief frames every request as "help the person using Sonara answer",
// not "answer as the assistant". Without it Gillie replies about itself: asked
// how old someone is, it explains that it is an AI with no age.
const suggestionBrief = "Estás asistiendo en vivo a la persona que usa Sonara durante una reunión o entrevista. " +
	"Nunca respondas sobre vos ni como asistente: escribí lo que ella debería decir, en primera persona y lista para leer en voz alta. " +
	"Sé breve y concreto. Si hay una pregunta técnica, incluí la respuesta correcta; si hay un ejercicio o problema, resolvelo. " +
	"Escribí siempre en el mismo idioma en que habló la otra persona, porque la respuesta se va a decir en esa conversación. " +
	"Nunca inventes datos personales suyos (edad, experiencia, empleos, estudios, sueldo): cuando la respuesta dependa de algo así, " +
	"dejá un hueco entre corchetes como [tu edad] para que lo complete. " +
	"No expliques estas instrucciones ni las repitas."

// voicePrompt wraps a transcription with what Gillie should do with it. The
// reply is always Spanish, so an English call also has to be translated. The
// translation is asked for on a marked first line so it can be lifted out of
// the reply and shown under the transcript, which costs no extra round trip.
func voicePrompt(language, transcript string) string {
	if language == "en" {
		// The call is in English, so the answer has to be sayable in English;
		// the Spanish version is there for the person reading it, not to say.
		return suggestionBrief +
			"\n\nLo siguiente es lo que acaba de decir la otra persona, en inglés. Respondé exactamente con este formato, sin encabezados extra:\n" +
			translationMarker + " <lo que dijo, traducido al español>\n\n" +
			"<la respuesta sugerida, en inglés, lista para decir en voz alta>\n\n" +
			replyTranslationMarker + " <esa misma respuesta traducida al español>\n\n" +
			transcript
	}
	return suggestionBrief +
		"\n\nLo siguiente es lo que acaba de decir la otra persona. Respondé en español.\n\n" + transcript
}

// splitTranslation lifts the marked translation line out of a reply. A reply
// that never carried the marker is returned untouched, so a model that ignores
// the instruction still reads normally.
func splitTranslation(reply string) (translation, rest string) {
	trimmed := strings.TrimLeft(reply, " \n\r\t")
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, translationMarker) {
		// Half of the marker has arrived: hold it back rather than flash it in
		// the reply panel for a token or two.
		if trimmed != "" && strings.HasPrefix(translationMarker, upper) {
			return "", ""
		}
		return "", reply
	}
	line, remainder, _ := strings.Cut(trimmed[len(translationMarker):], "\n")
	return strings.TrimSpace(line), strings.TrimLeft(remainder, " \n\r\t")
}

// capturePrompt is deliberately not built on suggestionBrief. A screenshot is
// read on screen, not said out loud, so the "short, first person, ready to read
// aloud" framing fights the one thing that matters here: a full worked solution.
const capturePrompt = "Asistís a la persona que usa Sonara durante una reunión o entrevista técnica. " +
	"Analizá esta captura de pantalla. Ignorá por completo cualquier interfaz, ventana, texto, controles, botones u overlay de Sonara " +
	"que aparezca en la imagen; no los menciones ni los uses como contexto. Analizá únicamente el contenido de la aplicación o llamada que está detrás.\n\n" +
	"SI HAY UN EJERCICIO, PROBLEMA O CÓDIGO, esto es lo único que tenés que devolver:\n" +
	"1. La solución completa dentro de un bloque de código, en el lenguaje del enunciado.\n" +
	"2. Un comentario propio en CADA línea del código, explicando qué hace y por qué se eligió así.\n" +
	"3. Una última línea con la complejidad temporal y espacial.\n" +
	"No lo resumas en prosa ni expliques el enfoque antes o después del bloque: el bloque de código ES la respuesta, por más largo que quede.\n" +
	"Escribí los comentarios como los escribiría la persona que resolvió el ejercicio mientras lo explica: concretos, en primera persona, " +
	"sin sonar a texto generado y sin repetir lo que el código ya dice solo.\n\n" +
	"Forma exacta que tiene que tener la respuesta (el contenido es de ejemplo, copiá solo la forma):\n" +
	"```python\n" +
	"def dos_sumas(nums, objetivo):        # recibo la lista y el objetivo tal como los pide el enunciado\n" +
	"    vistos = {}                       # acá guardo cada número ya recorrido con su índice, para no volver a recorrer\n" +
	"    for i, n in enumerate(nums):      # una sola pasada; necesito el índice porque el enunciado pide devolver posiciones\n" +
	"        faltante = objetivo - n       # si este número es parte de la solución, su pareja tiene que ser exactamente esto\n" +
	"        if faltante in vistos:        # lo busco en el diccionario, que es O(1), en vez de recorrer de nuevo\n" +
	"            return [vistos[faltante], i]\n" +
	"        vistos[n] = i                 # todavía no encontré la pareja, así que dejo este número disponible para los que siguen\n" +
	"# Tiempo O(n), espacio O(n): recorro una vez y en el peor caso guardo todos los números.\n" +
	"```\n\n" +
	"Si NO hay ejercicio, resumí en pocas líneas lo relevante para responder en la reunión. " +
	"Nunca inventes datos personales suyos: si la respuesta depende de alguno, dejá un hueco entre corchetes."

// AudioLevel reports the RMS of the last captured 100ms frame, normalised so
// the UI can draw the real signal instead of a decorative animation. It is
// polled far more often than ListeningStatus, so it stays a bare float.
func (a *App) AudioLevel() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return math.Min(1, a.audioLevel/meetLoudFrame)
}

func (a *App) setAudioLevel(level float64) {
	a.mu.Lock()
	a.audioLevel = level
	a.mu.Unlock()
}

func (a *App) IsListening() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.listening
}

type mangoClient struct {
	IsFloating bool `json:"is_floating"`
	IsOverlay  bool `json:"is_overlay"`
}

func overlayPlan(isOverlay, isFloating, weFloated bool) (cmds []string, nowFloated bool) {
	if isOverlay {
		cmds = append(cmds, "toggleoverlay")
		if weFloated {
			cmds = append(cmds, "togglefloating")
		}
		return cmds, false
	}
	if !isFloating {
		cmds = append(cmds, "togglefloating")
		weFloated = true
	}
	return append(cmds, "toggleoverlay"), weFloated
}

func (a *App) ToggleAlwaysOnTop() bool {
	out, err := exec.Command("mmsg", "get", "focusing-client").Output()
	if err != nil {
		return false
	}
	var c mangoClient
	if err := json.Unmarshal(out, &c); err != nil {
		return false
	}

	cmds, nowFloated := overlayPlan(c.IsOverlay, c.IsFloating, a.floatedForOverlay)
	for _, cmd := range cmds {
		if err := exec.Command("mmsg", "dispatch", cmd).Run(); err != nil {
			return false
		}
	}

	a.mu.Lock()
	a.floatedForOverlay = nowFloated
	a.mu.Unlock()
	return true
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
	// grim creates the file and then fills it, taking a few hundred
	// milliseconds for a 1080p frame. The UI polls for a new capture on a
	// timer, so writing in place lets it pick the file up at zero bytes and
	// upload that: Gillie then answers that the image arrived empty. Writing
	// beside it and renaming is atomic, so only the finished frame is ever
	// visible under the real name.
	pending := path + ".pending"
	if output, err := exec.Command("grim", pending).CombinedOutput(); err != nil {
		os.Remove(pending)
		return CaptureStatus{}, fmt.Errorf("grim failed: %w: %s", err, output)
	}
	if err := os.Rename(pending, path); err != nil {
		os.Remove(pending)
		return CaptureStatus{}, err
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
	// A capture left half written by an interrupted grim is worse than none:
	// it would be uploaded and analysed as an empty image.
	if info.Size() == 0 {
		return CaptureStatus{AgentReady: true}, nil
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
	if err := writer.WriteField("content", capturePrompt); err != nil {
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
	meetSampleRate    = 16000
	meetFrameBytes    = 3200 // 100ms mono s16 PCM
	meetSilenceFrames = 8    // 800ms of quiet ends an intervention
	meetMinimumFrames = 10   // ignore anything shorter than a second
	meetMaximumFrames = 150  // send after 15s of continuous speech
	meetVoiceFrames   = 5    // a segment needs half a second of actual voice
	meetStartFrames   = 3    // 300ms of continuous voice starts recording
	meetKeepSilence   = 3    // trailing frames kept so words are not clipped
	meetNoiseWindow   = 30   // 3s of quiet frames back the ambient estimate
	meetNoiseMargin   = 1.8  // how far above the ambient hiss a voice must sit
	meetNoiseFloor    = 0.004
	meetLoudFrame     = 0.08 // RMS treated as a full-height bar in the UI
)

// noiseFloor tracks the ambient level of the call so the voice threshold sits
// above whatever hiss this room, headset and codec produce rather than above a
// constant. Only frames that were not treated as voice feed the estimate, so a
// long intervention cannot drag the floor up behind itself.
type noiseFloor struct {
	quiet []float64
}

func (n *noiseFloor) observe(level float64) {
	n.quiet = append(n.quiet, level)
	if len(n.quiet) > meetNoiseWindow {
		n.quiet = n.quiet[len(n.quiet)-meetNoiseWindow:]
	}
}

// threshold is the median ambient level times a margin, never below the
// absolute floor: a perfectly silent stream must not make every frame voice.
func (n *noiseFloor) threshold() float64 {
	if len(n.quiet) == 0 {
		return meetNoiseFloor
	}
	sorted := append([]float64(nil), n.quiet...)
	sort.Float64s(sorted)
	return math.Max(sorted[len(sorted)/2]*meetNoiseMargin, meetNoiseFloor)
}

// trimTrailingSilence drops the silence that ended the segment, keeping a few
// frames so the last word is not clipped. A shorter upload is a faster
// transcription, and the trailing pause carries nothing.
func trimTrailingSilence(segment []byte, silenceFrames int) []byte {
	drop := silenceFrames - meetKeepSilence
	if drop <= 0 {
		return segment
	}
	trimmed := len(segment) - drop*meetFrameBytes
	if trimmed < meetMinimumFrames*meetFrameBytes {
		return segment
	}
	return segment[:trimmed]
}

// listenMeetAudio captures only Chrome/Chromium PipeWire output. It never falls
// back to the default system monitor, so unrelated desktop audio is excluded.
func (a *App) listenMeetAudio(ctx context.Context) {
	target, err := chromeAudioTarget(ctx)
	if err != nil {
		a.setListenStatus("Esperando audio de Chrome/Meet…")
		if waitForRetry(ctx) {
			go a.listenMeetAudio(ctx)
		}
		return
	}
	captureCtx, stopCapture := context.WithCancel(ctx)
	defer stopCapture()
	command := exec.CommandContext(captureCtx, "pw-record", "--target", target, "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	stdout, err := command.StdoutPipe()
	if err != nil || command.Start() != nil {
		a.setListeningFailure("No se pudo conectar a audio de Chrome/Meet")
		return
	}
	defer command.Wait()
	a.setListenStatus("Escuchando audio de Meet")

	// Chrome opens a new PipeWire node when a call starts playing, so the node
	// picked at connect time can stay silent forever. Watch for a better one and
	// drop the current recorder so the reconnect path retargets.
	var speaking atomic.Bool
	go watchChromeStream(captureCtx, target, &speaking, stopCapture)

	frame := make([]byte, meetFrameBytes)
	segment := make([]byte, 0, meetSampleRate*2*12)
	ambient := &noiseFloor{}
	recording, startFrames, voicedFrames, silenceFrames := false, 0, 0, 0
	for {
		if _, err := io.ReadFull(stdout, frame); err != nil {
			// Pausing cancels the context and kills the recorder, so this is
			// also where a segment cut short by the user gets sent.
			if recording && voicedFrames >= meetVoiceFrames {
				go a.deliverMeetSegment(append([]byte(nil), segment...))
			}
			a.setAudioLevel(0)
			if ctx.Err() == nil {
				a.setListenStatus("Stream de Chrome cambió; reconectando…")
				if waitForRetry(ctx) {
					go a.listenMeetAudio(ctx)
				}
			}
			return
		}
		level := pcmRMS(frame)
		a.setAudioLevel(level)
		voice := level >= ambient.threshold()
		if !voice {
			ambient.observe(level)
		}
		if !recording {
			if voice {
				startFrames++
				segment = append(segment, frame...)
				if startFrames >= meetStartFrames {
					recording, silenceFrames, voicedFrames = true, 0, startFrames
					speaking.Store(true)
					a.setListenStatus("Intervención detectada…")
				}
			} else {
				startFrames, segment = 0, segment[:0]
			}
			continue
		}
		segment = append(segment, frame...)
		if voice {
			silenceFrames = 0
			voicedFrames++
		} else {
			silenceFrames++
		}
		if silenceFrames < meetSilenceFrames && len(segment)/meetFrameBytes < meetMaximumFrames {
			continue
		}
		// A burst that never carried enough voice was background noise, not an
		// intervention: sending it only buys an empty transcription.
		if voicedFrames >= meetVoiceFrames && len(segment)/meetFrameBytes >= meetMinimumFrames {
			a.processMeetSegment(trimTrailingSilence(segment, silenceFrames))
		}
		recording, startFrames, voicedFrames, silenceFrames = false, 0, 0, 0
		speaking.Store(false)
		segment = segment[:0]
		if ctx.Err() != nil {
			return
		}
		a.setListenStatus("Escuchando audio de Meet")
	}
}

// watchChromeStream drops the current recorder when Chrome exposes a different
// output node, so a call that starts after the app connected is still captured.
func watchChromeStream(ctx context.Context, target string, speaking *atomic.Bool, stopCapture context.CancelFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		// Never cut a segment that is mid-sentence.
		if speaking.Load() {
			continue
		}
		current, err := chromeAudioTarget(ctx)
		if err != nil || current == target {
			continue
		}
		stopCapture()
		return
	}
}

func chromeAudioTarget(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return "", err
	}
	return pickChromeTarget(output)
}

func pickChromeTarget(output []byte) (string, error) {
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
	// Chrome keeps idle output nodes around, so prefer a running one and, among
	// equals, the newest — object.serial only ever grows, so the highest one is
	// the node a call just opened. The value returned is the node id: pw-record
	// resolves --target by id or name, and feeding it a serial silently links
	// nothing and records digital silence forever.
	best, bestRank, bestSerial := "", -1, float64(-1)
	for _, node := range nodes {
		if node.Type != "PipeWire:Interface:Node" || node.Info.Props["media.class"] != "Stream/Output/Audio" {
			continue
		}
		identity := strings.ToLower(fmt.Sprintf("%v %v %v", node.Info.Props["application.name"], node.Info.Props["application.process.binary"], node.Info.Props["node.name"]))
		if !strings.Contains(identity, "chrome") && !strings.Contains(identity, "chromium") && !strings.Contains(identity, "google-chrome") {
			continue
		}
		target, serial := fmt.Sprint(node.ID), float64(-1)
		if identifier, ok := node.ID.(float64); ok {
			target = fmt.Sprint(int64(identifier))
		}
		if value, ok := node.Info.Props["object.serial"].(float64); ok {
			serial = value
		}
		rank := 0
		if node.Info.State == "running" {
			rank = 1
		}
		if rank > bestRank || (rank == bestRank && serial > bestSerial) {
			best, bestRank, bestSerial = target, rank, serial
		}
	}
	if best == "" {
		return "", fmt.Errorf("Chrome no tiene stream de salida")
	}
	return best, nil
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
	audio := append([]byte(nil), pcm...) // the caller reuses its segment buffer
	a.mu.Lock()
	manual := a.manualSend
	if manual {
		a.pendingAudio = audio
		a.listenStatus = fmt.Sprintf("Fragmento de %.1fs listo para enviar", float64(len(audio))/float64(meetSampleRate*2))
	}
	a.mu.Unlock()
	if manual {
		return
	}
	a.deliverMeetSegment(audio)
}

// deliverMeetSegment transcribes the segment and then streams the reply, the
// same two-step the Gillie CLI uses in listen mode. The single blocking
// /message call it replaced returned nothing until the whole answer was
// generated, which made every intervention feel stalled.
func (a *App) deliverMeetSegment(pcm []byte) {
	conversationID, err := a.ensureGillieConversation()
	if err != nil {
		a.setListenStatus("Gillie no disponible: " + err.Error())
		return
	}
	language := a.audioLanguageOrDefault()
	a.setListenStatus("Gillie transcribiendo\u2026")
	transcript, err := a.transcribeMeetAudio(conversationID, language, wavFromPCM(pcm))
	if err != nil {
		a.setListenStatus("Error de transcripci\u00f3n: " + err.Error())
		return
	}
	if transcript == "" {
		a.setListenStatus("No se entendi\u00f3 la intervenci\u00f3n")
		return
	}

	a.mu.Lock()
	a.transcript = transcript
	a.translation, a.voiceReply = "", ""
	a.voiceSequence++ // opens a new reply the UI fills in as tokens arrive
	a.streaming = true
	a.listenStatus = "Gillie respondiendo\u2026"
	a.mu.Unlock()

	raw := ""
	err = a.streamGillieReply(conversationID, voicePrompt(language, transcript), func(token string) {
		raw += token
		translation, reply := splitTranslation(raw)
		a.mu.Lock()
		a.translation, a.voiceReply = translation, reply
		a.mu.Unlock()
	})

	a.mu.Lock()
	a.streaming = false
	if err != nil {
		a.listenStatus = "Error de respuesta: " + err.Error()
	} else {
		a.listenStatus = "Respuesta sugerida lista"
	}
	a.mu.Unlock()
}

func (a *App) transcribeMeetAudio(conversationID, language string, audio []byte) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// Names the language being spoken, not the one Gillie answers in.
	if err := writer.WriteField("language", language); err != nil {
		return "", err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="voice_content"; filename="meet-segment.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/conversation/%s/transcribe", gillieEndpoint(), conversationID), &body)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := (&http.Client{Timeout: 60 * time.Second}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Gillie transcribe respondi\u00f3 %s", response.Status)
	}
	var payload struct {
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Data.Text), nil
}

// streamGillieReply reads the SSE reply, handing every text token to onToken.
// Each data payload is JSON-encoded so tokens may contain newlines.
func (a *App) streamGillieReply(conversationID, content string, onToken func(string)) error {
	a.mu.RLock()
	visualContext := a.latestVisualContext
	a.mu.RUnlock()
	form := url.Values{"content": {content}}
	if visualContext != "" {
		form.Set("context", "Latest screenshot analysis from this Sonara session. Use it to answer follow-up questions directly when relevant:\n"+visualContext)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/conversation/%s/message/stream", gillieEndpoint(), conversationID),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/event-stream")
	response, err := (&http.Client{Timeout: 15 * time.Minute}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Gillie stream: %s", strings.TrimSpace(string(message)))
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		token, done := parseSSEToken(scanner.Text())
		if done {
			return nil
		}
		if token != "" {
			onToken(token)
		}
	}
	return scanner.Err()
}

// parseSSEToken turns one SSE line into a text token. Non-text events (the
// audio URLs the CLI plays) and keep-alives yield nothing.
func parseSSEToken(line string) (token string, done bool) {
	payload, found := strings.CutPrefix(line, "data: ")
	if !found {
		return "", false
	}
	if payload == "[DONE]" {
		return "", true
	}
	var decoded any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return payload, false // the CLI falls back to the raw payload too
	}
	text, ok := decoded.(string)
	if !ok {
		return "", false
	}
	if text == "[DONE]" {
		return "", true
	}
	return text, false
}

func wavFromPCM(pcm []byte) []byte {
	dataSize := uint32(len(pcm))
	wav := make([]byte, 44+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], 36+dataSize)
	copy(wav[8:16], "WAVEfmt ")
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

func waitForRetry(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(2 * time.Second):
		return true
	}
}
