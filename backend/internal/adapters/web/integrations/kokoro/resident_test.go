package kokoro

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sonara/internal/audio"
)

func TestResidentFallsBackOnceAndReusesCPUProcess(t *testing.T) {
	root := t.TempDir()
	gpu := filepath.Join(root, gpuRelease, "lib")
	if err := os.MkdirAll(gpu, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(gpu, "libonnxruntime_providers_cuda.so"), nil, 0600)
	fixture := filepath.Join(root, "fixture.wav")
	write(fixture, audio.WAV(make([]byte, 32000)), 0600)
	t.Setenv("KOKORO_TEST_WAV", fixture)
	starts := filepath.Join(root, "starts")
	t.Setenv("KOKORO_TEST_STARTS", starts)
	write(filepath.Join(root, "sonara-kokoro-worker"), []byte(`#!/bin/sh
printf '%s\n' "$3" >> "$KOKORO_TEST_STARTS"
[ "$3" = cpu ] || exit 1
printf 'ready\n'
while read -r sid speed size; do
  [ "$sid:$speed" = '28:0.850000' ] || exit 1
  dd bs=1 count="$size" of=/dev/null 2>/dev/null || exit 1
  cp "$KOKORO_TEST_WAV" "$4" || exit 1
  printf 'ok\n'
done
`), 0700)

	engine := &Engine{}
	defer engine.Close()
	voice := &Voice{dir: filepath.Join(root, model), lang: "es", sid: "28"}
	var note string
	for _, text := range []string{"¿Cómo estás?", "Otra pregunta."} {
		if _, err := engine.Speak(context.Background(), voice, text, .85, func(s string) { note = s }); err != nil {
			t.Fatal(err)
		}
	}
	started, err := os.ReadFile(starts)
	if err != nil || string(started) != "cuda\ncpu\n" || !strings.Contains(note, "CPU") {
		t.Fatalf("starts=%q note=%q err=%v", started, note, err)
	}
	dir := engine.worker.dir
	engine.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worker files survived Close: %v", err)
	}
}
