package assistant

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

type AnalyzeScreenUsecase interface {
	Execute(context.Context, AnalyzeScreenInput) (*domain.Response, error)
}

// ErrInvalidImage signals bad input, not a gateway failure.
var ErrInvalidImage = errors.New("image_data must be a base64 PNG, JPEG, or WebP data URL under 12MB")

const maxImageBytes = 12 << 20

func validImageData(value string) bool {
	prefixes := map[string]string{
		"data:image/png;base64,":  "png",
		"data:image/jpeg;base64,": "jpeg",
		"data:image/webp;base64,": "webp",
	}
	var format string
	var payload string
	for prefix, candidate := range prefixes {
		if strings.HasPrefix(value, prefix) {
			format = candidate
			payload = strings.TrimPrefix(value, prefix)
			break
		}
	}
	if format == "" || payload == "" || strings.ContainsAny(payload, " \t\r\n") || len(payload) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(decoded) == 0 || len(decoded) > maxImageBytes {
		return false
	}
	switch format {
	case "png":
		return len(decoded) >= 8 && string(decoded[:8]) == "\x89PNG\r\n\x1a\n"
	case "jpeg":
		return len(decoded) >= 3 && decoded[0] == 0xff && decoded[1] == 0xd8 && decoded[2] == 0xff
	case "webp":
		return len(decoded) >= 12 && string(decoded[:4]) == "RIFF" && string(decoded[8:12]) == "WEBP"
	default:
		return false
	}
}

type AnalyzeScreenInput struct {
	ImageData string `json:"image_data"`
	Prompt    string `json:"prompt"`
}

type screenUsecase struct {
	gateway responder
	cfg     config.Config
}

func NewAnalyzeScreenUsecase(gateway responder, cfg config.Config) AnalyzeScreenUsecase {
	return &screenUsecase{gateway: gateway, cfg: cfg}
}

func (u *screenUsecase) Execute(ctx context.Context, input AnalyzeScreenInput) (*domain.Response, error) {
	if !validImageData(input.ImageData) {
		return nil, ErrInvalidImage
	}
	prompt := input.Prompt
	if prompt == "" {
		prompt = "Analizá la captura. Si muestra una pregunta de entrevista o un problema de código, proponé una respuesta correcta y concreta. Ignorá overlays de Sonara."
	}
	in := domain.Request{Task: domain.TaskScreen, Prompt: prompt, ImageData: input.ImageData}
	text, err := u.gateway.Respond(ctx, in, u.cfg.ReasoningModel, "Sos el agente de visión de Sonara. Leé la captura y respondé solo sobre el contenido relevante de la entrevista o ejercicio.")
	if err != nil {
		return nil, err
	}
	return &domain.Response{Text: text, Agent: "screen_analyst", Model: u.cfg.ReasoningModel}, nil
}
