package client

import (
	"context"
	"fmt"
	"sonara/backend/usage"

	"sonara/backend/internal/adapters/web/integrations/providers"
	"sonara/backend/internal/adapters/web/integrations/realtime"
)

// LiveSession is a streaming transcription connection; LiveEvent and the
// LiveKind constants are what it emits. Aliased so callers outside this module
// can use them without reaching into internal packages.
type (
	LiveSession = realtime.Session
	LiveEvent   = realtime.Event
	LiveKind    = realtime.Kind
	VoiceEvent  = realtime.VoiceEvent
)

const (
	LiveDelta     = realtime.Delta
	LiveCommitted = realtime.Committed
	LiveCompleted = realtime.Completed
	LiveFailed    = realtime.Failed
	LiveClosed    = realtime.Closed
)

// OpenLive opens a streaming transcription session for the live copilot with
// the chosen live-transcription provider (OpenAI or Azure OpenAI). There is
// no fallback to another provider: a choice without streaming is an error
// that says so.
func (c *Client) OpenLive(ctx context.Context, language string) (*LiveSession, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Live)
	return c.dialLive(ctx, c.liveTranscribe.Provider(), language, "la transcripción en vivo")
}

// dialLive opens a streaming transcription session with provider: OpenAI's
// Realtime API or Azure OpenAI's GA one. Any other provider is an error; the
// caller then uploads audio in blocks instead.
func (c *Client) dialLive(ctx context.Context, provider, language, what string) (*LiveSession, error) {
	switch provider {
	case providers.OpenAI:
		c.mu.RLock()
		key, base := c.cfg.OpenAIKey, c.cfg.OpenAIBaseURL
		c.mu.RUnlock()
		endpoint, err := realtime.EndpointFor(base)
		if err != nil {
			return nil, err
		}
		// "minimal" delay: the whole point of this mode is latency.
		return realtime.Dial(ctx, realtime.Options{URL: endpoint, APIKey: key, Language: language, Delay: "minimal"})
	case providers.Azure:
		key, resource, deployment, live, ok := c.azure.RealtimeConfig()
		if !ok {
			return nil, fmt.Errorf("%s con Azure necesita clave, endpoint y deployment de transcripción", what)
		}
		endpoint, err := realtime.AzureEndpointFor(resource)
		if err != nil {
			return nil, err
		}
		return realtime.Dial(ctx, realtime.Options{URL: endpoint, APIKey: key, AzureKey: true, Model: deployment, LiveModel: live, Language: language})
	}
	return nil, fmt.Errorf("%s requiere OpenAI o Azure OpenAI como proveedor (ahora: %s)", what, provider)
}

// OpenMeetingLive is OpenLive for meeting transcription, with the meeting
// provider. Any provider without streaming is not an error the caller shows:
// it simply keeps uploading blocks.
func (c *Client) OpenMeetingLive(ctx context.Context, language string) (*LiveSession, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Meeting)
	return c.dialLive(ctx, c.meetingTranscribe.Provider(), language, "el streaming de reuniones")
}

// OpenPracticeVoice opens OpenAI's conversational Realtime API for Practice.
func (c *Client) OpenPracticeVoice(ctx context.Context, instructions string, speed float64) (*realtime.VoiceSession, error) {
	ctx = usage.With(ctx, c.usageRecorder, usage.Practice)
	c.mu.RLock()
	key, base := c.cfg.OpenAIKey, c.cfg.OpenAIBaseURL
	c.mu.RUnlock()
	return realtime.DialVoice(ctx, base, key, instructions, speed)
}
