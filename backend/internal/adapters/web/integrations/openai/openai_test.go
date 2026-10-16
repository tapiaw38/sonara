package openai

import (
	"strings"
	"testing"
)

func TestParseStreamEvent(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		delta   string
		done    bool
		failure string
	}{
		{"text delta", `data: {"type":"response.output_text.delta","delta":"hola"}`, "hola", false, ""},
		{"spaces survive", `data: {"type":"response.output_text.delta","delta":" con espacios "}`, " con espacios ", false, ""},
		{"newlines survive", `data: {"type":"response.output_text.delta","delta":"linea\nrota"}`, "linea\nrota", false, ""},
		{"completion", `data: {"type":"response.completed"}`, "", true, ""},
		{"sentinel", `data: [DONE]`, "", true, ""},
		{"no leading space", `data:{"type":"response.output_text.delta","delta":"x"}`, "x", false, ""},
		// Events we do not act on must not end the stream early.
		{"ignored event", `data: {"type":"response.output_item.added"}`, "", false, ""},
		{"event line", `event: response.output_text.delta`, "", false, ""},
		{"keep-alive", ``, "", false, ""},
		{"comment", `: ping`, "", false, ""},
		{"malformed json", `data: no-json`, "", false, ""},
		{"failure carries the message", `data: {"type":"response.failed","error":{"message":"rate limited"}}`, "", true, "rate limited"},
		{"failure without a message", `data: {"type":"error"}`, "", true, "OpenAI stream failed"},
	}
	for _, want := range cases {
		t.Run(want.name, func(t *testing.T) {
			delta, done, failure := parseStreamEvent(want.line)
			if delta != want.delta || done != want.done || failure != want.failure {
				t.Errorf("parseStreamEvent(%q) = (%q, %v, %q), want (%q, %v, %q)",
					want.line, delta, done, failure, want.delta, want.done, want.failure)
			}
		})
	}
}

func TestReadResponseStreamRequiresCompletion(t *testing.T) {
	partial := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hola\"}\n\n"
	var deltas []string
	text, err := readResponseStream(strings.NewReader(partial), func(delta string) { deltas = append(deltas, delta) })
	if text != "hola" || err == nil || len(deltas) != 1 {
		t.Fatalf("truncated stream = (%q, %v, %v), want partial text and error", text, err, deltas)
	}
	text, err = readResponseStream(strings.NewReader(partial+"data: {\"type\":\"response.completed\"}\n\n"), nil)
	if err != nil || text != "hola" {
		t.Fatalf("complete stream = (%q, %v), want hola", text, err)
	}
}
