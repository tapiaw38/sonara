package client

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"sonara/backend/internal/adapters/web/integrations/deepseek"
	"sonara/backend/internal/adapters/web/integrations/openai"
	"sonara/backend/internal/adapters/web/integrations/router"
	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
	"sonara/backend/internal/usecases/assistant"
)

type Client struct {
	mu         sync.RWMutex
	cfg        config.Config
	provider   openai.Gateway
	deepseek   *deepseek.Gateway
	respond    assistant.RespondUsecase
	screen     assistant.AnalyzeScreenUsecase
	transcribe assistant.TranscribeUsecase
	// meeting transcribes meeting recordings; it may use a different provider
	// from the live copilot's transcribe.
	meeting         assistant.TranscribeUsecase
	meetingProvider *router.Transcriber
	title           assistant.TitleUsecase
}

type Response struct {
	Text string
}

type Transcript struct {
	Text string
	// Diagnostics for classifying a slow call: see domain.Transcript.
	RequestID    string
	Status       int
	UploadBytes  int
	ProcessingMS int
	WaitMS       int
	TotalMS      int
}

func New() *Client {
	cfg := config.Load()
	provider := openai.NewGateway(cfg)
	cheap := deepseek.NewGateway(cfg)
	// Text and audio go to DeepSeek when preferred and keyed; anything with an
	// image, and any DeepSeek failure, goes to OpenAI.
	routed := router.New(provider, cheap, cheap.Configured, cfg.TextProvider)
	meeting := routed.Transcriber(cfg.MeetingProvider)
	return &Client{
		cfg: cfg, provider: provider, deepseek: cheap,
		respond:         assistant.NewRespondUsecase(routed, cfg),
		screen:          assistant.NewAnalyzeScreenUsecase(routed, cfg),
		transcribe:      assistant.NewTranscribeUsecase(routed.Transcriber(cfg.TranscribeProvider), cfg.TranscribeModel),
		meeting:         assistant.NewTranscribeUsecase(meeting, cfg.TranscribeModel),
		meetingProvider: meeting,
		title:           assistant.NewTitleUsecase(routed, cfg),
	}
}

// Title names a session from its opening exchange.
func (c *Client) Title(ctx context.Context, exchange string) (string, error) {
	return c.title.Execute(ctx, exchange)
}

func (c *Client) Status() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cfg.OpenAIKey == "" {
		return "Falta OPENAI_API_KEY"
	}
	return "API key configurada"
}

// SetDeepSeekKey is SetAPIKey for the cheaper provider; an empty key turns
// DeepSeek off and everything falls back to OpenAI.
func (c *Client) SetDeepSeekKey(key string) error {
	key = strings.TrimSpace(key)
	if key != "" && (len(key) < 20 || len(key) > 500 || strings.ContainsAny(key, " \t\r\n")) {
		return fmt.Errorf("API key de DeepSeek inválida")
	}
	c.deepseek.SetAPIKey(key)
	return nil
}

// MeetingProvider names who transcribes meeting audio right now, so the UI can
// say where the audio is going before recording starts.
func (c *Client) MeetingProvider() string { return c.meetingProvider.Provider() }

// TranscribeMeeting is Transcribe for meeting recordings.
func (c *Client) TranscribeMeeting(ctx context.Context, audio []byte, filename, language string) (Transcript, error) {
	return toTranscript(c.meeting.Execute(ctx, assistant.TranscribeInput{Audio: audio, Filename: filename, Language: language}))
}

// SetAPIKey updates key in memory only. It is never returned to the UI or persisted.
func (c *Client) SetAPIKey(key string) error {
	key = strings.TrimSpace(key)
	if key != "" {
		if len(key) < 20 || len(key) > 500 || strings.ContainsAny(key, " \t\r\n") {
			return fmt.Errorf("API key inválida")
		}
	}
	c.mu.Lock()
	c.cfg.OpenAIKey = key
	c.provider.SetAPIKey(key)
	c.mu.Unlock()
	return nil
}

func (c *Client) Respond(ctx context.Context, message, mode, sessionContext string) (Response, error) {
	output, err := c.respond.Execute(ctx, assistant.RespondInput{Message: message, Mode: mode, Context: sessionContext})
	if err != nil {
		return Response{}, err
	}
	return Response{Text: output.Text}, nil
}

// RespondStream is Respond with each token handed to onDelta as it arrives, so
// the UI can show the answer forming instead of waiting for the full reply.
func (c *Client) RespondStream(ctx context.Context, message, mode, sessionContext string, onDelta func(string)) (Response, error) {
	output, err := c.respond.ExecuteStream(ctx, assistant.RespondInput{Message: message, Mode: mode, Context: sessionContext}, onDelta)
	if err != nil {
		return Response{}, err
	}
	return Response{Text: output.Text}, nil
}

func (c *Client) AnalyzeScreen(ctx context.Context, imageData, prompt string) (Response, error) {
	output, err := c.screen.Execute(ctx, assistant.AnalyzeScreenInput{ImageData: imageData, Prompt: prompt})
	if err != nil {
		return Response{}, err
	}
	return Response{Text: output.Text}, nil
}

func (c *Client) Transcribe(ctx context.Context, audio []byte, filename, language string) (Transcript, error) {
	return toTranscript(c.transcribe.Execute(ctx, assistant.TranscribeInput{Audio: audio, Filename: filename, Language: language}))
}

func toTranscript(output *domain.Transcript, err error) (Transcript, error) {
	if output == nil {
		return Transcript{}, err
	}
	// Returned on the error path too, so a failure can still be classified.
	return Transcript{
		Text: output.Text, RequestID: output.RequestID, Status: output.Status,
		UploadBytes: output.UploadBytes, ProcessingMS: output.ProcessingMS,
		WaitMS: output.WaitMS, TotalMS: output.TotalMS,
	}, err
}
