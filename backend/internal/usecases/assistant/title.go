package assistant

import (
	"context"
	"strings"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

type TitleUsecase interface {
	Execute(context.Context, string) (string, error)
}

type titleUsecase struct {
	gateway responder
	cfg     config.Config
}

func NewTitleUsecase(gateway responder, cfg config.Config) TitleUsecase {
	return &titleUsecase{gateway: gateway, cfg: cfg}
}

// titleInstructions keeps the model from answering the exchange it is reading.
// Without the framing it tries to help with the interview question instead of
// naming it.
const titleInstructions = "Nombrás conversaciones. Devolvés solo un título corto, de 2 a 5 palabras, " +
	"en el idioma del texto, que diga de qué se trata. Sin comillas, sin punto final, sin prefijos como " +
	"\"Título:\". No respondas la pregunta que leas: solo titulala."

// Execute names a session from its opening exchange. It always uses the cheapest
// tier: a title is a label, not an answer, and it runs once per session.
func (u *titleUsecase) Execute(ctx context.Context, exchange string) (string, error) {
	exchange = strings.TrimSpace(exchange)
	if exchange == "" {
		return "", nil
	}
	text, err := u.gateway.Respond(ctx,
		domain.Request{Task: domain.TaskChat, Prompt: "Titulá esta conversación:\n\n" + exchange},
		u.cfg.FastModel, titleInstructions)
	if err != nil {
		return "", err
	}
	return cleanTitle(text), nil
}

// cleanTitle trims what models add anyway despite being told not to.
func cleanTitle(text string) string {
	title, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	title = strings.TrimSpace(title)
	for _, prefix := range []string{"Título:", "Titulo:", "Title:"} {
		title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
	}
	title = strings.Trim(title, "\"'“”«»")
	title = strings.TrimRight(title, ".")
	// A model that ignored the length limit would otherwise push the rest of the
	// row out of the sidebar.
	if len(title) > 60 {
		title = strings.TrimSpace(title[:60])
	}
	return title
}
