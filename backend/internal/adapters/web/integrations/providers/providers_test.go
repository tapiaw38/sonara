package providers

import (
	"context"
	"errors"
	"testing"

	"sonara/backend/internal/domain"
)

type fakeProvider struct {
	name      string
	ready     bool
	err       error
	calls     int
	responded string
}

func (f *fakeProvider) Configured() bool { return f.ready }

func (f *fakeProvider) Respond(context.Context, domain.Request, string, string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.name, nil
}

func (f *fakeProvider) RespondStream(context.Context, domain.Request, string, string, func(string)) (string, error) {
	f.calls++
	return f.name, f.err
}

func (f *fakeProvider) Transcribe(context.Context, []byte, string, string) (domain.Transcript, error) {
	f.calls++
	if f.err != nil {
		return domain.Transcript{}, f.err
	}
	return domain.Transcript{Text: f.name}, nil
}

func TestRegistryGetReturnsExactlyTheNamedProvider(t *testing.T) {
	a := &fakeProvider{name: "a", ready: true}
	b := &fakeProvider{name: "b", ready: true}
	registry := NewRegistry(map[string]Provider{"a": a, "b": b})

	got, err := registry.Get("a")
	if err != nil || got != a {
		t.Fatalf("got %v, %v; want provider a", got, err)
	}
	if _, err := registry.Get("missing"); err == nil {
		t.Fatal("an unknown name must error, not silently pick one of the existing providers")
	}
}

func TestRegistryGetFailsOnUnconfiguredProviderWithoutTryingAnother(t *testing.T) {
	broken := &fakeProvider{name: "broken", ready: false}
	other := &fakeProvider{name: "other", ready: true}
	registry := NewRegistry(map[string]Provider{"broken": broken, "other": other})

	if _, err := registry.Get("broken"); err == nil {
		t.Fatal("an unconfigured provider must error")
	}
	if other.calls != 0 {
		t.Fatal("Get must never touch a different provider than the one named")
	}
}

func TestSelectorRoutesByImageDataAndNeverFallsBack(t *testing.T) {
	text := &fakeProvider{name: "text", ready: true, err: errors.New("boom")}
	vision := &fakeProvider{name: "vision", ready: true}
	registry := NewRegistry(map[string]Provider{OpenAI: text, Azure: vision})
	selector := NewSelector(registry, OpenAI, Azure)

	if got, err := selector.Respond(context.Background(), domain.Request{ImageData: "data:image/png;base64,x"}, "m", "i"); err != nil || got != "vision" {
		t.Fatalf("vision request = %q, %v; want the vision provider used", got, err)
	}
	if _, err := selector.Respond(context.Background(), domain.Request{}, "m", "i"); err == nil {
		t.Fatal("a text request whose provider fails must surface the error, not retry on vision's provider")
	}
	if vision.calls != 1 {
		t.Fatalf("vision provider called %d times; a failed text request must never spill into it", vision.calls)
	}
}

func TestSelectorSettersChangeTheActiveChoice(t *testing.T) {
	a := &fakeProvider{name: "a", ready: true}
	b := &fakeProvider{name: "b", ready: true}
	registry := NewRegistry(map[string]Provider{"a": a, "b": b})
	selector := NewSelector(registry, "a", "a")

	selector.SetTextProvider("b")
	if selector.TextProvider() != "b" {
		t.Fatalf("TextProvider() = %q, want b", selector.TextProvider())
	}
	got, err := selector.Respond(context.Background(), domain.Request{}, "m", "i")
	if err != nil || got != "b" {
		t.Fatalf("got %q, %v; want provider b after switching", got, err)
	}
	if a.calls != 0 {
		t.Fatal("switching the choice must stop using the old provider entirely")
	}
}

func TestTranscriberIsBoundToOneProviderAndNeverFallsBack(t *testing.T) {
	primary := &fakeProvider{name: "primary", ready: true, err: errors.New("down")}
	secondary := &fakeProvider{name: "secondary", ready: true}
	registry := NewRegistry(map[string]Provider{"primary": primary, "secondary": secondary})
	transcriber := NewTranscriber(registry, "primary")

	if transcriber.Provider() != "primary" {
		t.Fatalf("Provider() = %q, want primary", transcriber.Provider())
	}
	if _, err := transcriber.Transcribe(context.Background(), nil, "", ""); err == nil {
		t.Fatal("a failing bound provider must error, not retry on another one")
	}
	if secondary.calls != 0 {
		t.Fatal("a bound Transcriber must never touch any provider but the one it is set to")
	}

	transcriber.SetProvider("secondary")
	out, err := transcriber.Transcribe(context.Background(), nil, "", "")
	if err != nil || out.Text != "secondary" {
		t.Fatalf("after switching: got %+v, %v", out, err)
	}
}

// splitProvider has chat and transcription configured independently, like Azure.
type splitProvider struct {
	fakeProvider
	transcribeReady bool
}

func (s *splitProvider) TranscribeConfigured() bool { return s.transcribeReady }

func TestTranscriberJudgesTranscriptionSetupNotChat(t *testing.T) {
	transcribeOnly := &splitProvider{fakeProvider: fakeProvider{name: "azure", ready: false}, transcribeReady: true}
	registry := NewRegistry(map[string]Provider{"azure": transcribeOnly})
	if _, err := NewTranscriber(registry, "azure").Transcribe(context.Background(), []byte("x"), "a.wav", "es"); err != nil {
		t.Fatalf("a transcription-only setup must transcribe: %v", err)
	}
	chatOnly := &splitProvider{fakeProvider: fakeProvider{name: "azure", ready: true}, transcribeReady: false}
	registry = NewRegistry(map[string]Provider{"azure": chatOnly})
	if _, err := NewTranscriber(registry, "azure").Transcribe(context.Background(), []byte("x"), "a.wav", "es"); err == nil {
		t.Fatal("a chat-only setup must be rejected before any transcription call")
	}
}
