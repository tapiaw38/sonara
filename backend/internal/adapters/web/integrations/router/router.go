// Package router sends each request to the cheapest provider that can serve it
// and falls back to OpenAI when that provider has no key or fails. Images never
// leave OpenAI.
package router

import (
	"context"

	"sonara/backend/internal/domain"
)

type Provider interface {
	Respond(context.Context, domain.Request, string, string) (string, error)
	RespondStream(context.Context, domain.Request, string, string, func(string)) (string, error)
	Transcribe(context.Context, []byte, string, string) (domain.Transcript, error)
}

const DeepSeek = "deepseek"

type Router struct {
	openai, deepseek Provider
	// deepseekReady reports whether DeepSeek has a key; without one the router
	// goes straight to OpenAI instead of failing first.
	deepseekReady func() bool
	textPref      string
}

func New(openai, deepseek Provider, deepseekReady func() bool, textPref string) *Router {
	return &Router{openai: openai, deepseek: deepseek, deepseekReady: deepseekReady, textPref: textPref}
}

// viaDeepSeek: ctx errors are the caller giving up, not a provider failure,
// so they never trigger a second paid request.
func (r *Router) viaDeepSeek(pref string) bool {
	return pref == DeepSeek && r.deepseekReady()
}

func (r *Router) Respond(ctx context.Context, in domain.Request, model, instructions string) (string, error) {
	if in.ImageData == "" && r.viaDeepSeek(r.textPref) {
		if text, err := r.deepseek.Respond(ctx, in, model, instructions); err == nil || ctx.Err() != nil {
			return text, err
		}
	}
	return r.openai.Respond(ctx, in, model, instructions)
}

func (r *Router) RespondStream(ctx context.Context, in domain.Request, model, instructions string, onDelta func(string)) (string, error) {
	if in.ImageData == "" && r.viaDeepSeek(r.textPref) {
		delivered := false
		text, err := r.deepseek.RespondStream(ctx, in, model, instructions, func(delta string) {
			delivered = true
			if onDelta != nil {
				onDelta(delta)
			}
		})
		// Once tokens reached the UI a fallback would replay the answer on top
		// of the partial one, so only an untouched stream may be retried.
		if err == nil || ctx.Err() != nil || delivered {
			return text, err
		}
	}
	return r.openai.RespondStream(ctx, in, model, instructions, onDelta)
}

// Transcriber binds a provider preference, so the live copilot and meeting
// recordings can use different providers through the same use case.
func (r *Router) Transcriber(pref string) *Transcriber { return &Transcriber{r: r, pref: pref} }

type Transcriber struct {
	r    *Router
	pref string
}

// Provider reports which provider the next call goes to first.
func (t *Transcriber) Provider() string {
	if t.r.viaDeepSeek(t.pref) {
		return DeepSeek
	}
	return "openai"
}

func (t *Transcriber) Transcribe(ctx context.Context, audio []byte, filename, language string) (domain.Transcript, error) {
	if t.r.viaDeepSeek(t.pref) {
		if out, err := t.r.deepseek.Transcribe(ctx, audio, filename, language); err == nil || ctx.Err() != nil {
			return out, err
		}
	}
	return t.r.openai.Transcribe(ctx, audio, filename, language)
}
