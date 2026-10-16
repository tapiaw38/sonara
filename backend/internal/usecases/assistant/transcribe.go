package assistant

import (
	"context"
	"sonara/backend/internal/domain"
)

type TranscribeUsecase interface {
	Execute(context.Context, TranscribeInput) (*domain.Transcript, error)
}

type TranscribeInput struct {
	Audio    []byte
	Filename string
	Language string
}

type transcriber interface {
	Transcribe(context.Context, []byte, string, string) (domain.Transcript, error)
}

type transcribeUsecase struct {
	gateway transcriber
	model   string
}

func NewTranscribeUsecase(gateway transcriber, model string) TranscribeUsecase {
	return &transcribeUsecase{gateway: gateway, model: model}
}

func (u *transcribeUsecase) Execute(ctx context.Context, input TranscribeInput) (*domain.Transcript, error) {
	out, err := u.gateway.Transcribe(ctx, input.Audio, input.Filename, input.Language)
	if err != nil {
		// The diagnostics are the point of this path, so they travel with the
		// failure too: a 429 and a slow upload both show up as "transcription
		// failed" otherwise.
		out.Model = u.model
		return &out, err
	}
	out.Model = u.model
	return &out, nil
}
