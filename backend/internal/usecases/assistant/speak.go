package assistant

import "context"

type SpeakUsecase interface {
	Execute(context.Context, SpeakInput) ([]byte, error)
}

type SpeakInput struct {
	Text string
}

type speaker interface {
	Speak(ctx context.Context, text, voice, model string) ([]byte, error)
}

type speakUsecase struct {
	gateway speaker
	model   string
	voice   string
}

func NewSpeakUsecase(gateway speaker, model, voice string) SpeakUsecase {
	return &speakUsecase{gateway: gateway, model: model, voice: voice}
}

func (u *speakUsecase) Execute(ctx context.Context, input SpeakInput) ([]byte, error) {
	return u.gateway.Speak(ctx, input.Text, u.voice, u.model)
}
