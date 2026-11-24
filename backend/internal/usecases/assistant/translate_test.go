package assistant

import (
	"context"
	"strings"
	"testing"

	"sonara/backend/internal/domain"
	"sonara/backend/internal/platform/config"
)

type recordingStreamer struct {
	model, instructions, prompt string
}

func (r *recordingStreamer) Respond(_ context.Context, in domain.Request, model, instructions string) (string, error) {
	r.model, r.instructions, r.prompt = model, instructions, in.Prompt
	return "Hello world", nil
}

func (r *recordingStreamer) RespondStream(_ context.Context, in domain.Request, model, instructions string, onDelta func(string)) (string, error) {
	r.model, r.instructions, r.prompt = model, instructions, in.Prompt
	onDelta("Hola ")
	onDelta("mundo")
	return "Hola mundo", nil
}

func TestTranslateAlwaysUsesTheFastTierAndStreams(t *testing.T) {
	gw := &recordingStreamer{}
	u := NewTranslateUsecase(gw, config.Config{FastModel: "fast", ReasoningModel: "slow"})
	var got strings.Builder
	// Mentions code on purpose: the copilot's agent routing would pick the
	// reasoning tier for this text; a translation must not.
	out, err := u.ExecuteStream(context.Background(), "I will implement the code tomorrow", func(d string) { got.WriteString(d) })
	if err != nil || out != "Hola mundo" || got.String() != "Hola mundo" {
		t.Fatalf("out=%q streamed=%q err=%v", out, got.String(), err)
	}
	if gw.model != "fast" {
		t.Errorf("model = %q, want the fast tier", gw.model)
	}
	if !strings.Contains(gw.prompt, "I will implement the code tomorrow") {
		t.Errorf("prompt lost the text: %q", gw.prompt)
	}
}

func TestTranslateEmptyTextSkipsTheCall(t *testing.T) {
	u := NewTranslateUsecase(&fakeResponder{err: context.Canceled}, config.Config{})
	if out, err := u.ExecuteStream(context.Background(), "  ", nil); out != "" || err != nil {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestTranslateWithoutDeltaUsesBufferedResponse(t *testing.T) {
	gw := &recordingStreamer{}
	u := NewTranslateUsecase(gw, config.Config{FastModel: "fast"})
	got, err := u.ExecuteTo(context.Background(), "Hola mundo", "en", nil)
	if err != nil || got != "Hello world" || gw.model != "fast" || !strings.Contains(gw.prompt, "Traducí al inglés") {
		t.Fatalf("translation=%q model=%q prompt=%q err=%v", got, gw.model, gw.prompt, err)
	}
}
