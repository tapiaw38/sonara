package assistant

import (
	"context"
	"strings"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

type RespondUsecase interface {
	Execute(context.Context, RespondInput) (*domain.Response, error)
	// ExecuteStream is Execute with the answer delivered token by token.
	ExecuteStream(context.Context, RespondInput, func(string)) (*domain.Response, error)
}

type RespondInput struct {
	Message string `json:"message"`
	Mode    string `json:"mode"`
	Context string `json:"context"`
}

type responder interface {
	Respond(context.Context, domain.Request, string, string) (string, error)
}

type streamResponder interface {
	RespondStream(context.Context, domain.Request, string, string, func(string)) (string, error)
}

type respondUsecase struct {
	gateway responder
	cfg     config.Config
}

func NewRespondUsecase(gateway responder, cfg config.Config) RespondUsecase {
	return &respondUsecase{gateway: gateway, cfg: cfg}
}

// plan resolves which agent, model and prompt a request gets. Both entry points
// go through it so a streamed answer is routed exactly like a buffered one.
func (u *respondUsecase) plan(input RespondInput) (request domain.Request, model, agent, instructions string) {
	mode := normalizeMode(input.Mode)
	agent, tier, instructions := chooseAgent(input.Message, mode)
	model = u.cfg.FastModel
	if tier == "reasoning" {
		model = u.cfg.ReasoningModel
	}
	if tier == "expert" {
		model = u.cfg.ExpertModel
	}
	prompt := input.Message
	if context := strings.TrimSpace(input.Context); context != "" {
		label := "Context from this Sonara session (use only when relevant)"
		if mode == "practice" || mode == "practice-turn" {
			label = "Interview context and conversation so far (use it)"
		}
		prompt = label + ":\n" + context + "\n\nLatest request:\n" + input.Message
	}
	return domain.Request{Task: domain.TaskChat, Mode: mode, Prompt: prompt}, model, agent, instructions
}

func (u *respondUsecase) Execute(ctx context.Context, input RespondInput) (*domain.Response, error) {
	request, model, agent, instructions := u.plan(input)
	text, err := u.gateway.Respond(ctx, request, model, instructions)
	if err != nil {
		return nil, err
	}
	return &domain.Response{Text: text, Agent: agent, Model: model}, nil
}

func (u *respondUsecase) ExecuteStream(ctx context.Context, input RespondInput, onDelta func(string)) (*domain.Response, error) {
	streamer, ok := u.gateway.(streamResponder)
	if !ok {
		return u.Execute(ctx, input)
	}
	request, model, agent, instructions := u.plan(input)
	text, err := streamer.RespondStream(ctx, request, model, instructions, onDelta)
	if err != nil {
		return nil, err
	}
	return &domain.Response{Text: text, Agent: agent, Model: model}, nil
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "coding", "coaching", "interview", "daily", "practice", "practice-turn", "summary", "memory":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "interview"
	}
}

func chooseAgent(message, mode string) (string, string, string) {
	if mode == "practice" || mode == "practice-turn" {
		instructions := "Sos quien entrevista en una conversación hablada. Leé tu pregunta anterior y la respuesta del candidato, junto con el contexto de la entrevista. Respondé con una reacción breve y natural a lo que dijo (sin elogios vacíos) y una sola pregunta bien elaborada que profundice en algo concreto de su respuesta o avance hacia el objetivo de la entrevista; en total dos o tres frases fáciles de decir en voz alta. Variá cómo enlazás las preguntas: a veces alcanza con preguntar directamente. Si te piden aclarar o repetir, hacelo antes de avanzar. Usá lenguaje cotidiano, frases breves y el idioma del candidato. Sin listas ni marcas de estilo." +
			" Si el contexto trae el perfil de la entrevista, actuá como alguien de esa empresa entrevistando para ese puesto, y elegí preguntas reales de ese tipo:" +
			" técnica, sobre el stack y los problemas del puesto, pidiendo detalle o un ejemplo concreto; conductual, sobre situaciones vividas, repreguntando por su rol, decisiones y resultado; diseño de sistemas, con un escenario que se va complicando (escala, fallas, trade-offs); producto y negocio, con casos, métricas y priorización; general, una mezcla." +
			" Ajustá la dificultad al nivel del puesto y apuntá al objetivo indicado. Usá la experiencia del candidato para preguntar por lo que dice haber hecho. No repitas temas ya cubiertos y subí la exigencia de a poco." +
			" Si una respuesta es vaga, repreguntá antes de cambiar de tema." +
			" En el contexto, las líneas \"me\" son respuestas que el candidato ya dio y las \"interviewer\" tus preguntas anteriores; el resumen recoge lo más viejo. Usalas para no repetir preguntas y para repreguntar sobre lo que dijo."
		if mode == "practice-turn" {
			instructions += " Escribí primero solo lo que diría el entrevistador. Luego escribí [[SUGGESTION]] en una línea aparte y redactá debajo lo que el candidato podría contestar a esa misma pregunta. Usá el mismo idioma en ambas partes." +
				" Empezá la sugerencia con una respuesta directa y desarrollala en dos a cuatro frases naturales cuando haga falta. Si preguntan por una experiencia, elegí un caso del perfil o de lo que la persona contó y conectá en pocas palabras el contexto, su aporte concreto y el resultado conocido." +
				" Si la pregunta es técnica, explicá un enfoque concreto y por qué sirve, con un ejemplo o una limitación pertinente. Si preguntan por una motivación, vinculá un interés concreto del candidato con el puesto." +
				" Escribí en primera persona, con lenguaje hablado y sin frases hechas. No inventes experiencia, cargos, métricas ni resultados. Si falta un dato esencial que solo el candidato puede aportar, dejá un marcador específico y breve entre corchetes. Si se puede responder con un enfoque hipotético útil, usalo. Entregá solo la respuesta lista para decir, sin títulos ni explicaciones."
		}
		return "interviewer", "fast", instructions
	}
	if mode == "memory" {
		return "memory", "fast", "Mantenés la memoria de una conversación en curso (entrevista o reunión). Recibís un resumen anterior y turnos nuevos, y devolvés un único resumen actualizado de hasta 150 palabras, en el idioma de la conversación. Incluí: de qué se habló y qué se preguntó; y una sección \"Lo que dije:\" con los datos concretos que afirmó \"me\" sobre sí misma (experiencia, tecnologías, números, decisiones, compromisos), para no contradecirlos después. Usá solo lo que está en el texto, sin inventar. Texto plano, sin markdown."
	}
	if mode == "summary" {
		return "summary", "fast", "Resumís la transcripción de una reunión en el idioma en que está escrita. Empezá con un párrafo corto de qué se trató, después los temas principales, las decisiones y acuerdos, y los pendientes con su responsable si se menciona. Usá solo lo que está en la transcripción; si algo no quedó claro, decilo. Texto plano, con guiones para las listas, sin markdown."
	}
	if mode == "coding" || containsAny(strings.ToLower(message), "algoritmo", "complexity", "complejidad", "leetcode", "debug", "código", "code", "implement") {
		return "coding", "reasoning", "Actuás como agente técnico de Sonara. Resolvés problemas de programación con precisión y explicás brevemente la solución, complejidad temporal y espacial."
	}
	if containsAny(strings.ToLower(message), "system design", "arquitectura distribuida", "consistencia eventual", "consensus", "consenso") {
		return "expert", "expert", "Actuás como agente experto de diseño de sistemas. Proponés una solución escalable, explicitás tradeoffs y respondés con criterio técnico."
	}
	if mode == "coaching" {
		return "coach", "fast", "Actuás como coach de entrevistas. Das feedback concreto, amable y breve, con una sugerencia accionable."
	}
	if mode == "daily" {
		return "meeting", "fast", "Ayudás a la persona a comunicarse con claridad en una reunión cotidiana. Respondé con lenguaje natural, breve y profesional; no inventes datos personales."
	}
	return "interview", "fast", "Asistís a la persona que usa Sonara en una entrevista. Redactá una respuesta breve, natural y lista para decir. No inventes experiencia ni datos personales; usá [corchetes] si falta información."
}

func containsAny(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}
