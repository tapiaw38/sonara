package kokoro

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"sonara/backend/usage"
)

const model = "kokoro-int8-multi-lang-v1_0"
const release = "sherpa-onnx-v1.12.40-linux-x64-static"

type Voice struct {
	bin, dir, lang, sid string
}

func Open(language string) (*Voice, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return openAt(filepath.Join(home, ".local", "share", "sherpa-onnx"), language)
}

func openAt(root, language string) (*Voice, error) {
	voice := &Voice{
		bin:  filepath.Join(root, release, "bin", "sherpa-onnx-offline-tts"),
		dir:  filepath.Join(root, model),
		lang: "es", sid: "28", // ef_dora
	}
	if strings.EqualFold(language, "en") {
		voice.lang, voice.sid = "en-us", "2" // af_bella
	}
	if _, workerErr := exec.LookPath(filepath.Join(root, "sonara-kokoro-worker")); workerErr != nil {
		if _, cliErr := exec.LookPath(voice.bin); cliErr != nil {
			return nil, fmt.Errorf("Kokoro local requiere el proceso residente o %s. Instalá Kokoro según el README", voice.bin)
		}
	}
	for _, file := range []string{"model.int8.onnx", "voices.bin", "tokens.txt", "espeak-ng-data"} {
		if _, err := os.Stat(filepath.Join(voice.dir, file)); err != nil {
			return nil, fmt.Errorf("falta %s en %s. Instalá el modelo Kokoro int8 según el README", file, voice.dir)
		}
	}
	return voice, nil
}

func (e *Engine) Speak(ctx context.Context, v *Voice, text string, speed float64, note func(string)) (_ []byte, callErr error) {
	if len(text) == 0 || len(text) > 65536 || strings.ContainsRune(text, 0) || !(speed >= 0.5 && speed <= 1.5) {
		return nil, fmt.Errorf("texto o velocidad inválidos para Kokoro")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meter := usage.Start(ctx, "local", model, usage.TTS)
	meter.Characters = int64(utf8.RuneCountInString(text))
	defer func() { meter.Finish(callErr == nil) }()
	wav, err := e.synthesize(ctx, v, text, speed, note)
	if err != nil {
		return nil, err
	}
	meter.AudioSeconds = usage.WAVSeconds(wav)
	if meter.AudioSeconds == 0 {
		return nil, fmt.Errorf("Kokoro local no devolvió un WAV válido")
	}
	return wav, nil
}

func (v *Voice) speakCLI(ctx context.Context, text string, speed float64) ([]byte, error) {
	dir, err := os.MkdirTemp("", "sonara-kokoro-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "question.wav")
	cmd := exec.CommandContext(ctx, v.bin,
		"--kokoro-model="+filepath.Join(v.dir, "model.int8.onnx"),
		"--kokoro-voices="+filepath.Join(v.dir, "voices.bin"),
		"--kokoro-tokens="+filepath.Join(v.dir, "tokens.txt"),
		"--kokoro-data-dir="+filepath.Join(v.dir, "espeak-ng-data"),
		"--kokoro-lang="+v.lang, "--sid="+v.sid, "--num-threads=2",
		"--kokoro-length-scale="+strconv.FormatFloat(1/speed, 'f', 6, 64),
		"--output-filename="+output, "--", text)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("síntesis de Kokoro local: %w", ctx.Err())
		}
		return nil, fmt.Errorf("Kokoro local no pudo generar voz: %w", err)
	}
	wav, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("Kokoro local no generó el archivo WAV")
	}
	return wav, nil
}
