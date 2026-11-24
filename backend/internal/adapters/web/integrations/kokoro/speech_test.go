package kokoro

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonara/backend/usage"
	"sonara/internal/audio"
)

func TestLocalVoiceSynthesizesAndRecordsUsage(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, release, "bin")
	modelDir := filepath.Join(root, model)
	for _, dir := range []string{bin, filepath.Join(modelDir, "espeak-ng-data")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"model.int8.onnx", "tokens.txt", "voices.bin"} {
		if err := os.WriteFile(filepath.Join(modelDir, file), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := filepath.Join(root, "fixture.wav")
	if err := os.WriteFile(fixture, audio.WAV(make([]byte, 32000)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KOKORO_TEST_WAV", fixture)
	executable := `#!/bin/sh
for arg do
  case "$arg" in
    --kokoro-lang=*) lang=${arg#*=} ;;
    --sid=*) sid=${arg#*=} ;;
    --kokoro-length-scale=*) scale=${arg#*=} ;;
    --output-filename=*) out=${arg#*=} ;;
  esac
done
case "$lang:$sid:$scale:$arg" in
  "es:28:1.176471:¿Listo?"|"en-us:2:1.176471:Ready?") cp "$KOKORO_TEST_WAV" "$out" ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "sherpa-onnx-offline-tts"), []byte(executable), 0700); err != nil {
		t.Fatal(err)
	}

	for _, example := range []struct{ language, text string }{{"es", "¿Listo?"}, {"en", "Ready?"}} {
		voice, err := openAt(root, example.language)
		if err != nil {
			t.Fatal(err)
		}
		var records []usage.Entry
		ctx := usage.With(context.Background(), func(e usage.Entry) { records = append(records, e) }, usage.TTS)
		if _, err := voice.Speak(ctx, example.text, 0.85); err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || records[0].Provider != "local" || records[0].Function != usage.TTS ||
			records[0].Model != model || records[0].AudioSeconds != 1 || records[0].Characters == 0 || !records[0].OK {
			t.Fatalf("unexpected usage: %+v", records)
		}
		if _, err := voice.Speak(ctx, "force failure", 0.85); err == nil || len(records) != 2 || records[1].OK {
			t.Fatalf("failed synthesis not recorded: %v, %+v", err, records)
		}
	}

	if err := os.Remove(filepath.Join(modelDir, "voices.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := openAt(root, "es"); err == nil || !strings.Contains(err.Error(), "voices.bin") {
		t.Fatalf("missing model file: %v", err)
	}
}

func TestLocalVoiceReportsMissingExecutable(t *testing.T) {
	if _, err := openAt(t.TempDir(), "es"); err == nil || !strings.Contains(err.Error(), release) {
		t.Fatalf("missing executable: %v", err)
	}
}
