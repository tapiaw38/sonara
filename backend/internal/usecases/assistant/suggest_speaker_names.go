package assistant

import (
	"context"
	"encoding/json"
	"strings"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

type SuggestSpeakerNamesUsecase interface {
	// Execute reads a meeting transcript (speaker turns labeled with the
	// caller's own tokens, e.g. "spk_0") and returns a token->name map for
	// only the speakers who were named or who introduced themselves in the
	// conversation itself. A speaker with no clear mention is simply absent
	// from the result — this never invents a name.
	Execute(ctx context.Context, transcript string) (map[string]string, error)
}

type suggestSpeakerNamesUsecase struct {
	gateway responder
	cfg     config.Config
}

func NewSuggestSpeakerNamesUsecase(gateway responder, cfg config.Config) SuggestSpeakerNamesUsecase {
	return &suggestSpeakerNamesUsecase{gateway: gateway, cfg: cfg}
}

// suggestNamesInstructions is deliberately strict about inventing nothing: a
// wrong guess here renames a real person, so silence is the safe default.
const suggestNamesInstructions = "Leés la transcripción de una reunión. Cada línea empieza con una etiqueta " +
	"de hablante (por ejemplo \"spk_0\" o \"generico\") seguida de lo que dijo. Buscá únicamente nombres " +
	"propios que la propia conversación menciona de forma explícita: alguien que se presenta (\"soy María\"), " +
	"o es saludado o nombrado por otro (\"Hola Pedro\", \"gracias Juan\"). Cuando alguien saluda o le habla a un nombre, ese nombre es de quien responde a continuación, no de quien lo dijo. Nunca inventes ni deduzcas un " +
	"nombre que no esté dicho en el texto; ante la duda, omitilo. Respondé solo un objeto JSON plano donde " +
	"cada clave es exactamente una etiqueta que aparece en la transcripción y el valor es el nombre propio " +
	"encontrado, con mayúscula inicial. Omití toda etiqueta sin un nombre claro: no incluyas la clave en ese " +
	"caso. No agregues texto antes o después del JSON, ni la etiqueta \"Yo\"."

func (u *suggestSpeakerNamesUsecase) Execute(ctx context.Context, transcript string) (map[string]string, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return map[string]string{}, nil
	}
	text, err := u.gateway.Respond(ctx,
		domain.Request{Task: domain.TaskChat, Prompt: transcript},
		u.cfg.FastModel, suggestNamesInstructions)
	if err != nil {
		return nil, err
	}
	raw := map[string]string{}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &raw); err != nil {
		// A model that ignored the format is a missing feature, not a broken
		// meeting: no suggestions rather than a surfaced error.
		return map[string]string{}, nil
	}
	names := map[string]string{}
	for key, name := range raw {
		if name = strings.TrimSpace(name); name != "" {
			names[key] = name
		}
	}
	return names, nil
}

// extractJSONObject strips the markdown code fence a model adds despite being
// told not to, and anything before/after the first {...} block.
func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return text
	}
	return text[start : end+1]
}
