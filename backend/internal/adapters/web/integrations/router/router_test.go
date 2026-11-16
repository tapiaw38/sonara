package router

import (
	"context"
	"errors"
	"testing"

	"sonara/backend/internal/domain"
)

type fake struct {
	name  string
	err   error
	calls int
	// deltas are streamed before err is returned.
	deltas []string
}

func (f *fake) Respond(context.Context, domain.Request, string, string) (string, error) {
	f.calls++
	return f.name, f.err
}

func (f *fake) RespondStream(_ context.Context, _ domain.Request, _, _ string, on func(string)) (string, error) {
	f.calls++
	for _, d := range f.deltas {
		on(d)
	}
	return f.name, f.err
}

func (f *fake) Transcribe(context.Context, []byte, string, string) (domain.Transcript, error) {
	f.calls++
	return domain.Transcript{Text: f.name}, f.err
}

func TestRouting(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	cases := []struct {
		name      string
		pref      string
		ready     bool
		image     string
		deepErr   error
		want      string
		deepCalls int
	}{
		{"text goes to deepseek", DeepSeek, true, "", nil, "deepseek", 1},
		{"image stays on openai", DeepSeek, true, "data:image/png;base64,AA", nil, "openai", 0},
		{"no key skips deepseek", DeepSeek, false, "", nil, "openai", 0},
		{"openai preference", "openai", true, "", nil, "openai", 0},
		{"failure falls back", DeepSeek, true, "", boom, "openai", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deep, open := &fake{name: "deepseek", err: c.deepErr}, &fake{name: "openai"}
			r := New(open, deep, func() bool { return c.ready }, c.pref)
			got, err := r.Respond(ctx, domain.Request{ImageData: c.image}, "m", "i")
			if err != nil || got != c.want || deep.calls != c.deepCalls {
				t.Fatalf("got %q err=%v deepCalls=%d, want %q calls=%d", got, err, deep.calls, c.want, c.deepCalls)
			}
		})
	}
}

func TestStreamFallbackOnlyBeforeFirstToken(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	deep := &fake{name: "deepseek", err: boom, deltas: []string{"hola"}}
	open := &fake{name: "openai"}
	r := New(open, deep, func() bool { return true }, DeepSeek)
	if _, err := r.RespondStream(ctx, domain.Request{}, "m", "i", func(string) {}); !errors.Is(err, boom) || open.calls != 0 {
		t.Fatalf("partial stream must not fall back: err=%v openaiCalls=%d", err, open.calls)
	}
	deep.deltas = nil
	if got, err := r.RespondStream(ctx, domain.Request{}, "m", "i", func(string) {}); err != nil || got != "openai" {
		t.Fatalf("untouched stream should fall back: got %q err=%v", got, err)
	}
}

func TestCanceledContextDoesNotFallBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deep, open := &fake{name: "deepseek", err: context.Canceled}, &fake{name: "openai"}
	tr := New(open, deep, func() bool { return true }, "openai").Transcriber(DeepSeek)
	if _, err := tr.Transcribe(ctx, nil, "", ""); err == nil || open.calls != 0 {
		t.Fatalf("canceled call must not retry on openai: err=%v calls=%d", err, open.calls)
	}
	if tr.Provider() != DeepSeek {
		t.Fatal("provider should report deepseek when ready")
	}
}
