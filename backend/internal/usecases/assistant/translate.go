package assistant

import (
	"context"
	"strings"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

// TranslateUsecase translates what the other person just said into Spanish.
// It is its own use case, apart from RespondUsecase, on purpose: the copilot's
// agent routing picks a slower reasoning model for anything that mentions code,
// and a translation must never pay that latency. It always uses the fast tier.
type TranslateUsecase interface {
	// ExecuteStream hands each token to onDelta as it arrives and returns the
	// full translation.
	ExecuteStream(ctx context.Context, text string, onDelta func(string)) (string, error)
}

type translateUsecase struct {
	gateway responder
	cfg     config.Config
}

func NewTranslateUsecase(gateway responder, cfg config.Config) TranslateUsecase {
	return &translateUsecase{gateway: gateway, cfg: cfg}
}

const translateInstructions = "Sos un traductor. Traducís al español, de forma fiel y natural, lo que dijo " +
	"otra persona en una reunión. Devolvés solo la traducción: sin comillas, sin notas, sin explicaciones, " +
	"y sin responder a lo que dice."

func (u *translateUsecase) ExecuteStream(ctx context.Context, text string, onDelta func(string)) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	request := domain.Request{Task: domain.TaskChat, Prompt: "Traducí al español:\n\n" + text}
	if streamer, ok := u.gateway.(streamResponder); ok {
		return streamer.RespondStream(ctx, request, u.cfg.FastModel, translateInstructions, onDelta)
	}
	out, err := u.gateway.Respond(ctx, request, u.cfg.FastModel, translateInstructions)
	if err == nil && onDelta != nil {
		onDelta(out)
	}
	return out, err
}
