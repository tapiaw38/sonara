// Package providers is a Strategy: every backend (OpenAI, DeepSeek, Azure
// OpenAI, ...) implements the same Provider interface, and a Registry just
// looks one up by name. There is deliberately no fallback chain anywhere in
// here — picking a provider for a capability (text, vision, transcription)
// is a single explicit choice made in Settings and persisted in the
// database, never a chain that silently retries with a different one when
// the chosen provider fails or lacks a key. A failure is returned to the
// caller as-is, naming the provider that failed, so it is visible instead of
// hidden behind a quieter answer from someone else.
package providers

import (
	"context"
	"fmt"
	"sync"

	"sonara/backend/internal/domain"
)

// Provider is the strategy interface. in.ImageData decides vision vs text
// inside Respond/RespondStream; a provider that cannot serve vision (for
// example DeepSeek) returns its own clear error for that case.
type Provider interface {
	Respond(context.Context, domain.Request, string, string) (string, error)
	RespondStream(context.Context, domain.Request, string, string, func(string)) (string, error)
	Transcribe(context.Context, []byte, string, string) (domain.Transcript, error)
}

// configured is implemented by a provider that can report whether it has
// what it needs (a key, an endpoint, …), so Get can fail with "not
// configured" instead of a cryptic error from the HTTP call itself.
type configured interface {
	Configured() bool
}

// Names of the providers Sonara ships. A name not in this list is still
// accepted by NewRegistry (so an integration can add its own), but Settings
// only ever offers these.
const (
	OpenAI   = "openai"
	DeepSeek = "deepseek"
	Azure    = "azure"
	Local    = "local"
)

// Registry is a fixed set of named strategies, built once at startup.
type Registry struct {
	byName map[string]Provider
}

func NewRegistry(byName map[string]Provider) *Registry {
	return &Registry{byName: byName}
}

// Get returns exactly the named provider, never a substitute. An unknown
// name or one reporting itself unconfigured is an error, not a fallback.
func (r *Registry) Get(name string) (Provider, error) {
	provider, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("proveedor desconocido: %q", name)
	}
	if checker, ok := provider.(configured); ok && !checker.Configured() {
		return nil, fmt.Errorf("el proveedor %q no está configurado", name)
	}
	return provider, nil
}

// Selector resolves a chat request to one configured name: visionProvider
// when the request carries an image, textProvider otherwise. Each is one
// explicit choice, changeable at runtime from Settings.
type Selector struct {
	registry *Registry
	mu       sync.RWMutex
	text     string
	vision   string
}

func NewSelector(registry *Registry, textProvider, visionProvider string) *Selector {
	return &Selector{registry: registry, text: textProvider, vision: visionProvider}
}

func (s *Selector) SetTextProvider(name string)   { s.mu.Lock(); s.text = name; s.mu.Unlock() }
func (s *Selector) SetVisionProvider(name string) { s.mu.Lock(); s.vision = name; s.mu.Unlock() }
func (s *Selector) TextProvider() string          { s.mu.RLock(); defer s.mu.RUnlock(); return s.text }
func (s *Selector) VisionProvider() string        { s.mu.RLock(); defer s.mu.RUnlock(); return s.vision }

func (s *Selector) nameFor(in domain.Request) string {
	if in.ImageData != "" {
		return s.VisionProvider()
	}
	return s.TextProvider()
}

func (s *Selector) Respond(ctx context.Context, in domain.Request, model, instructions string) (string, error) {
	provider, err := s.registry.Get(s.nameFor(in))
	if err != nil {
		return "", err
	}
	return provider.Respond(ctx, in, model, instructions)
}

func (s *Selector) RespondStream(ctx context.Context, in domain.Request, model, instructions string, onDelta func(string)) (string, error) {
	provider, err := s.registry.Get(s.nameFor(in))
	if err != nil {
		return "", err
	}
	return provider.RespondStream(ctx, in, model, instructions, onDelta)
}

// Transcriber is bound to exactly one provider name. The live copilot and
// meeting recordings each get their own Transcriber, so they can be pointed
// at different providers independently — still never a chain.
type Transcriber struct {
	registry *Registry
	mu       sync.RWMutex
	name     string
}

func NewTranscriber(registry *Registry, name string) *Transcriber {
	return &Transcriber{registry: registry, name: name}
}

func (t *Transcriber) SetProvider(name string) { t.mu.Lock(); t.name = name; t.mu.Unlock() }

// Provider names which provider the next call goes to — the only one it
// will try.
func (t *Transcriber) Provider() string { t.mu.RLock(); defer t.mu.RUnlock(); return t.name }

func (t *Transcriber) Transcribe(ctx context.Context, audio []byte, filename, language string) (domain.Transcript, error) {
	return t.TranscribeWithPrompt(ctx, audio, filename, language, "")
}

func (t *Transcriber) TranscribeWithPrompt(ctx context.Context, audio []byte, filename, language, prompt string) (domain.Transcript, error) {
	provider, err := t.registry.Get(t.Provider())
	if err != nil {
		return domain.Transcript{}, err
	}
	if prompted, ok := provider.(interface {
		TranscribeWithPrompt(context.Context, []byte, string, string, string) (domain.Transcript, error)
	}); ok && prompt != "" {
		return prompted.TranscribeWithPrompt(ctx, audio, filename, language, prompt)
	}
	return provider.Transcribe(ctx, audio, filename, language)
}
