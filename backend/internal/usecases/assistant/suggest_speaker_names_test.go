package assistant

import (
	"context"
	"testing"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

type fakeResponder struct {
	text string
	err  error
}

func (f *fakeResponder) Respond(context.Context, domain.Request, string, string) (string, error) {
	return f.text, f.err
}

func TestSuggestSpeakerNamesParsesCleanJSON(t *testing.T) {
	u := NewSuggestSpeakerNamesUsecase(&fakeResponder{text: `{"spk_0":"Pedro","spk_1":""}`}, config.Config{})
	got, err := u.Execute(context.Background(), "spk_0: Hola, soy Pedro.\nspk_1: Hola.")
	if err != nil {
		t.Fatal(err)
	}
	if got["spk_0"] != "Pedro" {
		t.Errorf("got %v, want spk_0=Pedro", got)
	}
	if _, ok := got["spk_1"]; ok {
		t.Errorf("an empty name must be dropped, got %v", got)
	}
}

func TestSuggestSpeakerNamesStripsMarkdownFence(t *testing.T) {
	u := NewSuggestSpeakerNamesUsecase(&fakeResponder{text: "```json\n{\"spk_0\":\"Ana\"}\n```"}, config.Config{})
	got, err := u.Execute(context.Background(), "spk_0: soy Ana")
	if err != nil || got["spk_0"] != "Ana" {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestSuggestSpeakerNamesIgnoresMalformedOutput(t *testing.T) {
	u := NewSuggestSpeakerNamesUsecase(&fakeResponder{text: "no sé quién habla"}, config.Config{})
	got, err := u.Execute(context.Background(), "spk_0: hola")
	if err != nil || len(got) != 0 {
		t.Fatalf("malformed output must yield no suggestions, not an error: got %v, err %v", got, err)
	}
}

func TestSuggestSpeakerNamesEmptyTranscriptSkipsTheCall(t *testing.T) {
	// A responder that always errors: if Execute reached it despite the empty
	// transcript, this test would fail on the error instead of passing.
	u := NewSuggestSpeakerNamesUsecase(&fakeResponder{err: context.Canceled}, config.Config{})
	got, err := u.Execute(context.Background(), "   ")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestSuggestSpeakerNamesPropagatesGatewayError(t *testing.T) {
	u := NewSuggestSpeakerNamesUsecase(&fakeResponder{err: context.DeadlineExceeded}, config.Config{})
	if _, err := u.Execute(context.Background(), "spk_0: hola"); err == nil {
		t.Fatal("a gateway failure must propagate, not be swallowed as zero suggestions")
	}
}
