package client

import (
	"context"
	"fmt"
	"sonara/backend/usage"
	"strings"
	"sync"

	"sonara/backend/internal/adapters/web/integrations/azureopenai"
	"sonara/backend/internal/adapters/web/integrations/deepseek"
	"sonara/backend/internal/adapters/web/integrations/localwhisper"
	"sonara/backend/internal/adapters/web/integrations/openai"
	"sonara/backend/internal/adapters/web/integrations/providers"
	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
	"sonara/backend/internal/usecases/assistant"
)

// Client wires one Strategy per capability (text, vision, live-copilot
// transcription, meeting transcription): each resolves to exactly one named
// provider, chosen in Settings and persisted there — never an automatic
// fallback chain. See providers.Registry/Selector/Transcriber.
type Client struct {
	usageRecorder         usage.Recorder
	practiceVoiceProvider string
	mu                    sync.RWMutex
	cfg                   config.Config
	provider              openai.Gateway
	deepseek              *deepseek.Gateway
	azure                 *azureopenai.Gateway
	local                 *localwhisper.Gateway

	textVision        *providers.Selector
	liveTranscribe    *providers.Transcriber
	meetingTranscribe *providers.Transcriber

	respond      assistant.RespondUsecase
	screen       assistant.AnalyzeScreenUsecase
	transcribe   assistant.TranscribeUsecase
	meeting      assistant.TranscribeUsecase
	title        assistant.TitleUsecase
	suggestNames assistant.SuggestSpeakerNamesUsecase
	translate    assistant.TranslateUsecase
	speak        assistant.SpeakUsecase
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

// AzureConfig mirrors azureopenai.Config: kept as its own type here so
// callers outside this module (main package) never import the integration
// package directly.
type AzureConfig struct {
	APIKey               string
	Endpoint             string
	ChatAPIVersion       string
	TranscribeAPIVersion string
	ChatDeployment       string
	TranscribeDeployment string
	LiveDeployment       string
	SpeechDeployment     string
}

func New() *Client { return NewWithUsage(nil) }

func NewWithUsage(record usage.Recorder) *Client {
	cfg := config.Load()
	provider := openai.NewGateway(cfg)
	cheap := deepseek.NewGateway(cfg)
	azure := azureopenai.NewGateway()
	local := localwhisper.NewGateway()
	registry := providers.NewRegistry(map[string]providers.Provider{
		providers.OpenAI:   provider,
		providers.DeepSeek: cheap,
		providers.Azure:    azure,
		providers.Local:    local,
	})
	// Every capability defaults to OpenAI until Settings says otherwise —
	// one explicit choice each, loaded from the database at startup
	// (main package's App), never from the environment.
	textVision := providers.NewSelector(registry, providers.OpenAI, providers.OpenAI)
	liveTranscribe := providers.NewTranscriber(registry, providers.OpenAI)
	meetingTranscribe := providers.NewTranscriber(registry, providers.OpenAI)
	return &Client{
		usageRecorder: record, cfg: cfg, provider: provider, deepseek: cheap, azure: azure, local: local,
		textVision: textVision, liveTranscribe: liveTranscribe, meetingTranscribe: meetingTranscribe,
		respond:      assistant.NewRespondUsecase(textVision, cfg),
		screen:       assistant.NewAnalyzeScreenUsecase(textVision, cfg),
		transcribe:   assistant.NewTranscribeUsecase(liveTranscribe, cfg.TranscribeModel),
		meeting:      assistant.NewTranscribeUsecase(meetingTranscribe, cfg.TranscribeModel),
		title:        assistant.NewTitleUsecase(textVision, cfg),
		suggestNames: assistant.NewSuggestSpeakerNamesUsecase(textVision, cfg),
		translate:    assistant.NewTranslateUsecase(textVision, cfg),
		speak:        assistant.NewSpeakUsecase(provider, cfg.TTSModel, cfg.TTSVoice),
	}
}

// Title names a session from its opening exchange.
func (c *Client) Title(ctx context.Context, exchange string) (string, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Summary)
	return c.title.Execute(ctx, exchange)
}

// SuggestSpeakerNames reads a meeting transcript (see
// assistant.SuggestSpeakerNamesUsecase for the token format) and returns
// real names found in the conversation itself, never invented. Uses
// whichever provider TextProvider currently names.
func (c *Client) SuggestSpeakerNames(ctx context.Context, transcript string) (map[string]string, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Names)
	return c.suggestNames.Execute(ctx, transcript)
}

// TranslateStream translates what the other person said into Spanish on the
// fast tier, token by token. Independent of RespondStream so a reply and its
// translation can run at the same time.
func (c *Client) TranslateStream(ctx context.Context, text string, onDelta func(string)) (string, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Translation)
	return c.translate.ExecuteStream(ctx, text, onDelta)
}

// TranslateTo returns a translation into Spanish or English.
func (c *Client) TranslateTo(ctx context.Context, text, language string) (string, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Translation)
	return c.translate.ExecuteTo(ctx, text, language, nil)
}

// Close stops the local Whisper server if Sonara started it.
func (c *Client) Close() { c.local.Stop() }

func (c *Client) Status() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cfg.OpenAIKey == "" {
		return "Falta OPENAI_API_KEY"
	}
	return "API key configurada"
}

// SetDeepSeekKey is SetAPIKey for the DeepSeek provider. Does not change
// which capability uses DeepSeek — see SetTextProvider et al.
func (c *Client) SetDeepSeekKey(key string) error {
	key = strings.TrimSpace(key)
	if key != "" && (len(key) < 20 || len(key) > 500 || strings.ContainsAny(key, " \t\r\n")) {
		return fmt.Errorf("API key de DeepSeek inválida")
	}
	c.deepseek.SetAPIKey(key)
	return nil
}

// SetAzureOpenAIConfig sets every Azure setting as one unit (Settings has a
// single save action for the whole form). An empty APIKey turns Azure off:
// Get will then report it unconfigured to any capability still pointed at it.
func (c *Client) SetAzureOpenAIConfig(cfg AzureConfig) error {
	key := strings.TrimSpace(cfg.APIKey)
	if key != "" && (len(key) < 10 || len(key) > 500 || strings.ContainsAny(key, " \t\r\n")) {
		return fmt.Errorf("clave de Azure OpenAI inválida")
	}
	c.azure.SetConfig(azureopenai.Config{
		APIKey: key, Endpoint: cfg.Endpoint,
		ChatAPIVersion: cfg.ChatAPIVersion, TranscribeAPIVersion: cfg.TranscribeAPIVersion,
		ChatDeployment: cfg.ChatDeployment, TranscribeDeployment: cfg.TranscribeDeployment,
		LiveDeployment: cfg.LiveDeployment, SpeechDeployment: cfg.SpeechDeployment,
	})
	return nil
}

// validProviderName rejects anything but the three providers Settings can
// offer: a typo must error, never silently land on a default.
func validProviderName(name string) error {
	switch name {
	case providers.OpenAI, providers.DeepSeek, providers.Azure, providers.Local:
		return nil
	default:
		return fmt.Errorf("proveedor desconocido: %q", name)
	}
}

// SetTextProvider chooses who answers a text request. Never a chain: from
// now on, exactly this provider, until changed again.
func (c *Client) SetTextProvider(name string) error {
	if err := validProviderName(name); err != nil {
		return err
	}
	c.textVision.SetTextProvider(name)
	return nil
}

func (c *Client) TextProvider() string { return c.textVision.TextProvider() }

// TextProviderReady says whether the chosen text provider has what it needs
// (a key, an endpoint) — not whether that key is valid.
func (c *Client) TextProviderReady() bool { return c.ProviderReady(c.textVision.TextProvider()) }

// ProviderReady says whether a provider has what it needs for text (a key,
// an endpoint), not whether that key is valid. Local needs nothing.
func (c *Client) ProviderReady(name string) bool {
	switch name {
	case providers.OpenAI:
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.cfg.OpenAIKey != ""
	case providers.DeepSeek:
		return c.deepseek.Configured()
	case providers.Azure:
		return c.azure.Configured()
	}
	return true
}

// AzureTranscribeReady is ProviderReady for Azure transcription, which has
// its own deployment.
func (c *Client) AzureTranscribeReady() bool { return c.azure.TranscribeConfigured() }

// SetVisionProvider chooses who answers a request that carries a screen
// capture. DeepSeek is accepted here (Settings may still offer it), but it
// will error on every vision call, since it only serves text.
func (c *Client) SetVisionProvider(name string) error {
	if err := validProviderName(name); err != nil {
		return err
	}
	c.textVision.SetVisionProvider(name)
	return nil
}

func (c *Client) VisionProvider() string { return c.textVision.VisionProvider() }

// SetLiveTranscribeProvider chooses who transcribes the interview copilot's
// audio, independently of meeting transcription.
func (c *Client) SetLiveTranscribeProvider(name string) error {
	if err := validProviderName(name); err != nil {
		return err
	}
	c.liveTranscribe.SetProvider(name)
	return nil
}

func (c *Client) LiveTranscribeProvider() string { return c.liveTranscribe.Provider() }

// SetMeetingProvider chooses who transcribes meeting recordings.
func (c *Client) SetMeetingProvider(name string) error {
	if err := validProviderName(name); err != nil {
		return err
	}
	c.meetingTranscribe.SetProvider(name)
	return nil
}

// MeetingProvider names who transcribes meeting audio right now, so the UI
// can say where the audio is going before recording starts.
func (c *Client) MeetingProvider() string { return c.meetingTranscribe.Provider() }

// TranscribeMeeting is Transcribe for meeting recordings.
func (c *Client) TranscribeMeeting(ctx context.Context, audio []byte, filename, language string) (Transcript, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Meeting)
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
	ctx = usage.With(ctx, c.usageRecorder, responseFunction(mode))
	output, err := c.respond.Execute(ctx, assistant.RespondInput{Message: message, Mode: mode, Context: sessionContext})
	if err != nil {
		return Response{}, err
	}
	return Response{Text: output.Text}, nil
}

// RespondStream is Respond with each token handed to onDelta as it arrives, so
// the UI can show the answer forming instead of waiting for the full reply.
func (c *Client) RespondStream(ctx context.Context, message, mode, sessionContext string, onDelta func(string)) (Response, error) {
	ctx = usage.With(ctx, c.usageRecorder, responseFunction(mode))
	output, err := c.respond.ExecuteStream(ctx, assistant.RespondInput{Message: message, Mode: mode, Context: sessionContext}, onDelta)
	if err != nil {
		return Response{}, err
	}
	return Response{Text: output.Text}, nil
}

func (c *Client) AnalyzeScreen(ctx context.Context, imageData, prompt string) (Response, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Copilot)
	output, err := c.screen.Execute(ctx, assistant.AnalyzeScreenInput{ImageData: imageData, Prompt: prompt})
	if err != nil {
		return Response{}, err
	}
	return Response{Text: output.Text}, nil
}

func (c *Client) Transcribe(ctx context.Context, audio []byte, filename, language string) (Transcript, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Live)
	return toTranscript(c.transcribe.Execute(ctx, assistant.TranscribeInput{Audio: audio, Filename: filename, Language: language}))
}

// TranscribePractice gives the selected transcriber context for spoken interview answers.
func (c *Client) TranscribePractice(ctx context.Context, audio []byte, language string) (Transcript, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Practice)
	prompt := "Respuesta espontánea de una persona en una entrevista. Transcribí lo que realmente se oye, con puntuación natural. Conservá nombres, siglas, vocabulario técnico y cambios de idioma. No completes palabras dudosas ni agregues texto durante el silencio."
	if language == "en" {
		prompt = "A candidate speaking spontaneously in an interview. Transcribe only what is audible, with natural punctuation. Preserve names, acronyms, technical terms, and language switches. Do not fill in unclear words or add text during silence."
	}
	output, err := c.liveTranscribe.TranscribeWithPrompt(ctx, audio, "answer.wav", language, prompt)
	return toTranscript(&output, err)
}

// Speak synthesizes through the configured Practice voice provider.
func (c *Client) Speak(ctx context.Context, text string) ([]byte, error) {
	if c.PracticeVoiceProvider() == providers.Azure {
		return c.SpeakAzurePractice(ctx, text)
	}
	ctx = usage.With(ctx, c.usageRecorder, usage.TTS)
	return c.speak.Execute(ctx, assistant.SpeakInput{Text: text})
}

func (c *Client) PracticeVoiceProvider() string {
	c.mu.RLock()
	name := c.practiceVoiceProvider
	c.mu.RUnlock()
	if name == "" {
		if c.TextProvider() == providers.Azure {
			return providers.Azure
		}
		return providers.OpenAI
	}
	return name
}

func (c *Client) SetPracticeVoiceProvider(name string) error {
	if name != providers.OpenAI && name != providers.Azure {
		return fmt.Errorf("proveedor de voz inválido")
	}
	c.mu.Lock()
	c.practiceVoiceProvider = name
	c.mu.Unlock()
	return nil
}

func (c *Client) CheckAzurePractice() error {
	if err := c.azure.CheckSpeech(); err != nil {
		return err
	}
	if !c.azure.TranscribeConfigured() {
		return fmt.Errorf("Práctica con voz Azure requiere un deployment de transcripción de Azure")
	}
	return nil
}

// The running Azure session keeps its selected audio provider until it ends.
func (c *Client) SpeakAzurePractice(ctx context.Context, text string) ([]byte, error) {
	return c.azure.Speak(usage.With(ctx, c.usageRecorder, usage.TTS), text, "alloy", "")
}

func (c *Client) TranscribeAzurePractice(ctx context.Context, wav []byte, language string) (Transcript, error) {
	output, err := c.azure.Transcribe(usage.With(ctx, c.usageRecorder, usage.Practice), wav, "practice.wav", language)
	return toTranscript(&output, err)
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

func responseFunction(mode string) string {
	switch mode {
	case "practice", "practice-turn", "practice-question", "practice-suggestion":
		return usage.Practice
	case "summary", "memory":
		return usage.Summary
	default:
		return usage.Copilot
	}
}
