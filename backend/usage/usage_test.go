package usage

import (
	"context"
	"testing"
)

func TestMeterRecordsStreamingUsageAndRoute(t *testing.T) {
	var entries []Entry
	m := Start(With(context.Background(), func(e Entry) { entries = append(entries, e) }, Translation), "azure", "deployment", Copilot)
	ObserveSSE([]byte(`data: {"choices":[],"usage":{"prompt_tokens":30,"completion_tokens":12}}`), m)
	m.Finish(true)
	if len(entries) != 1 || entries[0].Function != Translation || entries[0].Model != "deployment" ||
		entries[0].Input != 30 || entries[0].Output != 12 || !entries[0].OK || !entries[0].TokensKnown {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestRealtimeAndGeminiUsageSchemas(t *testing.T) {
	ctx := With(context.Background(), func(Entry) {}, Practice)
	m := Start(ctx, "openai", "realtime", Practice)
	m.Observe([]byte(`{"response":{"usage":{"input_tokens":100,"output_tokens":80,"input_token_details":{"audio_tokens":60},"output_token_details":{"audio_tokens":70}}}}`))
	if m.Input != 100 || m.Output != 80 || m.AudioInput != 60 || m.AudioOutput != 70 {
		t.Fatalf("%+v", m.Entry)
	}
	g := Start(ctx, "gemini", "hydrogen", Diarization)
	g.Observe([]byte(`{"usage":{"total_input_tokens":200,"total_output_tokens":10,"total_thought_tokens":15}}`))
	if g.Input != 200 || g.Output != 25 {
		t.Fatalf("%+v", g.Entry)
	}
}
