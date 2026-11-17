package client

import (
	"context"
	"fmt"

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
)

const (
	LiveDelta     = realtime.Delta
	LiveCommitted = realtime.Committed
	LiveCompleted = realtime.Completed
	LiveFailed    = realtime.Failed
	LiveClosed    = realtime.Closed
)

// OpenLive opens a streaming transcription session for the live copilot.
// Streaming exists only on OpenAI, so it requires OpenAI to be the chosen
// live-transcription provider: there is no fallback to another provider, a
// wrong choice is an error that says so.
func (c *Client) OpenLive(ctx context.Context, language string) (*LiveSession, error) {
	if name := c.liveTranscribe.Provider(); name != providers.OpenAI {
		return nil, fmt.Errorf("la transcripción en vivo requiere OpenAI como proveedor de transcripción en vivo (ahora: %s)", name)
	}
	c.mu.RLock()
	key, base := c.cfg.OpenAIKey, c.cfg.OpenAIBaseURL
	c.mu.RUnlock()
	endpoint, err := realtime.EndpointFor(base)
	if err != nil {
		return nil, err
	}
	// "minimal" delay: the whole point of this mode is latency.
	return realtime.Dial(ctx, realtime.Options{URL: endpoint, APIKey: key, Language: language, Delay: "minimal"})
}
