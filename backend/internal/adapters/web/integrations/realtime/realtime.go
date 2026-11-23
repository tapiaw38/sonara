// Package realtime streams microphone/meeting audio to OpenAI's Realtime
// transcription endpoint and turns its server events into a plain Go channel.
//
// It exists to take the upload-and-wait round trip out of the copilot's
// latency: text appears while the other person is still talking (delta
// events) and the final transcript arrives right after the turn is committed,
// instead of after a whole WAV has been uploaded.
//
// gpt-live-transcribe has no server-side turn detection: the caller decides
// where a turn ends (Commit). It returns no speaker labels or timestamps.
//
// *** UNVERIFIED AGAINST THE REAL SERVICE ***: the event and session shapes
// follow OpenAI's public docs (developers.openai.com/api/docs/guides/
// realtime-transcription, fetched 2026-11-17) and are exercised only against a
// local fake server in realtime_test.go.
package realtime

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Kind int

const (
	// Delta is a partial piece of the transcript of the item being spoken.
	Delta Kind = iota + 1
	// Committed confirms a Commit: the item id its transcript will carry.
	// Committed events arrive in commit order, one per commit.
	Committed
	// Completed is the final transcript of one item.
	Completed
	// Failed is a transcription or protocol error (Err set; ItemID when known).
	Failed
	// Closed is always the last event: the connection ended (Err may be nil).
	Closed
)

type Event struct {
	Kind   Kind
	ItemID string
	Text   string
	Err    error
}

// DefaultModel is OpenAI's streaming transcription model.
const DefaultModel = "gpt-live-transcribe"

type Options struct {
	// URL is the full wss:// endpoint, including ?intent=transcription.
	URL    string
	APIKey string
	// AzureKey sends APIKey as Azure's api-key header instead of OpenAI's
	// Authorization: Bearer.
	AzureKey bool
	// LiveModel marks Model as a gpt-live-transcribe deployment under another
	// name (Azure): it gets that model's session fields.
	LiveModel bool
	Model     string // default DefaultModel; on Azure, the transcription deployment
	// Language is a single ISO 639-1 code ("en", "es"); empty lets the model decide.
	Language string
	// Delay trades latency for accuracy: minimal|low|medium|high|xhigh.
	Delay string
}

// EndpointFor turns an HTTP API base URL (https://api.openai.com/v1) into the
// Realtime transcription WebSocket URL.
func EndpointFor(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("URL base inválida: %q", baseURL)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path += "/realtime"
	u.RawQuery = "intent=transcription"
	return u.String(), nil
}

// AzureEndpointFor turns an Azure OpenAI resource URL
// (https://name.openai.azure.com) into its Realtime transcription WebSocket.
// Only the GA /openai/v1 path accepts transcription sessions; the preview
// /openai/realtime?api-version= one answers realtime_beta_access_required
// (checked against a real resource, 2026-11-22).
func AzureEndpointFor(resource string) (string, error) {
	u, err := url.Parse(strings.TrimRight(resource, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("endpoint de Azure inválido: %q", resource)
	}
	u.Scheme, u.Path, u.RawQuery = "wss", "/openai/v1/realtime", "intent=transcription"
	return u.String(), nil
}

type Session struct {
	conn         *websocket.Conn
	events       chan Event
	writeMu      sync.Mutex
	writeTimeout time.Duration
	rs           resampler
	closing      sync.Once
	cancel       context.CancelFunc
}

// Dial connects, configures the transcription session and starts reading
// server events. Events() is closed after a final Closed event.
func Dial(ctx context.Context, opts Options) (*Session, error) {
	if opts.APIKey == "" {
		return nil, errors.New("falta la clave para la transcripción en vivo")
	}
	header := http.Header{"Authorization": []string{"Bearer " + opts.APIKey}}
	if opts.AzureKey {
		header = http.Header{"api-key": []string{opts.APIKey}}
	}
	conn, err := dialWithRetry(ctx, opts.URL, header)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s := &Session{conn: conn, events: make(chan Event, 128), cancel: cancel}
	if err := s.send(sessionUpdate(opts)); err != nil {
		conn.Close()
		cancel()
		return nil, err
	}
	go s.read(runCtx)
	return s, nil
}

// dialRetryDelays spaces the handshake attempts. A var so tests do not wait.
var dialRetryDelays = []time.Duration{300 * time.Millisecond, 800 * time.Millisecond}

// dialWithRetry retries a handshake that died on the way (EOF, reset) or that
// the server refused for now (5xx, 429): the first connection after a while
// idle sometimes drops (seen on Azure). A definitive answer such as 401 or 403
// fails at once, since retrying a bad key only delays the error.
func dialWithRetry(ctx context.Context, url string, header http.Header) (*websocket.Conn, error) {
	for attempt := 0; ; attempt++ {
		conn, resp, err := websocket.DefaultDialer.DialContext(ctx, url, header)
		if err == nil {
			return conn, nil
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		transient := status == 0 || status >= 500 || status == http.StatusTooManyRequests
		if !transient || attempt >= len(dialRetryDelays) || ctx.Err() != nil {
			if status != 0 {
				return nil, fmt.Errorf("no se pudo abrir la transcripción en vivo (estado %d): %w", status, err)
			}
			return nil, fmt.Errorf("no se pudo abrir la transcripción en vivo: %w", err)
		}
		select {
		case <-time.After(dialRetryDelays[attempt]):
		case <-ctx.Done():
			return nil, fmt.Errorf("no se pudo abrir la transcripción en vivo: %w", ctx.Err())
		}
	}
}

func sessionUpdate(opts Options) map[string]any {
	model := opts.Model
	if model == "" {
		model = DefaultModel
	}
	transcription := map[string]any{"model": model}
	// languages and delay are gpt-live-transcribe's. The gpt-4o-*-transcribe
	// models take a single language and reject delay, which fails the whole
	// session.update (seen on Azure).
	if model == DefaultModel || opts.LiveModel {
		if opts.Language != "" {
			transcription["languages"] = []string{opts.Language}
		}
		// Azure rejects delay even for gpt-live-transcribe, failing the
		// whole session.update.
		if opts.Delay != "" && !opts.AzureKey {
			transcription["delay"] = opts.Delay
		}
	} else if opts.Language != "" {
		transcription["language"] = opts.Language
	}
	return map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "transcription",
			"audio": map[string]any{"input": map[string]any{
				"format":         map[string]any{"type": "audio/pcm", "rate": 24000},
				"transcription":  transcription,
				"turn_detection": nil,
			}},
		},
	}
}

func (s *Session) send(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeTimeout > 0 {
		if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
			return err
		}
	}
	return s.conn.WriteJSON(v)
}

// Append streams 16 kHz mono s16 PCM (what the recorder produces); it is
// resampled to the 24 kHz the endpoint expects.
func (s *Session) Append(pcm16k []byte) error {
	s.writeMu.Lock()
	out := s.rs.convert(pcm16k)
	s.writeMu.Unlock()
	return s.send(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(out)})
}

// Commit ends the current turn: the server transcribes what was appended.
func (s *Session) Commit() error {
	return s.send(map[string]any{"type": "input_audio_buffer.commit"})
}

// Clear drops what was appended since the last commit (silence, mostly).
func (s *Session) Clear() error {
	return s.send(map[string]any{"type": "input_audio_buffer.clear"})
}

func (s *Session) Events() <-chan Event { return s.events }

func (s *Session) Close() error {
	var err error
	s.closing.Do(func() {
		s.cancel()
		err = s.conn.Close()
	})
	return err
}

type serverEvent struct {
	Type       string `json:"type"`
	ItemID     string `json:"item_id"`
	Delta      string `json:"delta"`
	Transcript string `json:"transcript"`
	Error      *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (s *Session) read(ctx context.Context) {
	defer close(s.events)
	emit := func(e Event) bool {
		select {
		case s.events <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				err = nil // we closed it ourselves
			}
			emit(Event{Kind: Closed, Err: err})
			return
		}
		var ev serverEvent
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		var out Event
		switch ev.Type {
		case "conversation.item.input_audio_transcription.delta":
			out = Event{Kind: Delta, ItemID: ev.ItemID, Text: ev.Delta}
		case "conversation.item.input_audio_transcription.completed":
			out = Event{Kind: Completed, ItemID: ev.ItemID, Text: ev.Transcript}
		case "input_audio_buffer.committed":
			out = Event{Kind: Committed, ItemID: ev.ItemID}
		case "conversation.item.input_audio_transcription.failed", "error":
			msg := "error de transcripción en vivo"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
				if ev.Error.Code != "" {
					msg = ev.Error.Code + ": " + msg
				}
			}
			out = Event{Kind: Failed, ItemID: ev.ItemID, Err: errors.New(msg)}
		default:
			continue
		}
		if !emit(out) {
			return
		}
	}
}

// resampler converts 16 kHz s16le mono to 24 kHz by linear interpolation,
// carrying the last sample across calls so frame edges do not click. Speech
// intelligibility is what matters here, not audio fidelity.
type resampler struct {
	last    int16
	hasLast bool
	// phase is the fractional input position (in thirds of a sample) the next
	// output sample starts at, carried between frames.
	phase int
}

func (r *resampler) convert(in []byte) []byte {
	n := len(in) / 2
	if n == 0 {
		return nil
	}
	sample := func(i int) float64 {
		if i < 0 {
			if r.hasLast {
				return float64(r.last)
			}
			return float64(int16(binary.LittleEndian.Uint16(in[0:2])))
		}
		if i >= n {
			i = n - 1
		}
		return float64(int16(binary.LittleEndian.Uint16(in[i*2 : i*2+2])))
	}
	// Output step is 2/3 of an input sample (16k -> 24k = x1.5).
	out := make([]byte, 0, n*3+4)
	for pos := r.phase; pos < n*3; pos += 2 {
		i, frac := pos/3-0, float64(pos%3)/3
		a, b := sample(i), sample(i+1)
		v := int16(a*(1-frac) + b*frac)
		out = binary.LittleEndian.AppendUint16(out, uint16(v))
		r.phase = pos + 2 - n*3
	}
	r.last, r.hasLast = int16(sample(n-1)), true
	return out
}
