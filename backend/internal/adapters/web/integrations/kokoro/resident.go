package kokoro

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const gpuRelease = "sherpa-onnx-v1.12.40-cuda-12.x-cudnn-9.x-linux-x64-gpu"

type Engine struct {
	mu         sync.Mutex
	worker     *resident
	cudaFailed bool
	closed     bool
}

type resident struct {
	cmd                     *exec.Cmd
	input                   io.WriteCloser
	output                  *bufio.Reader
	dir, language, provider string
}

func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.stop()
}

func (e *Engine) stop() {
	if e.worker == nil {
		return
	}
	w := e.worker
	e.worker = nil
	w.input.Close()
	_ = w.cmd.Process.Kill()
	_ = w.cmd.Wait()
	_ = os.RemoveAll(w.dir)
}

func (e *Engine) synthesize(ctx context.Context, v *Voice, text string, speed float64, note func(string)) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("Kokoro ya se cerró")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.worker != nil && e.worker.language != v.lang {
		e.stop()
	}

	root := filepath.Dir(v.dir)
	bin := filepath.Join(root, "sonara-kokoro-worker")
	if _, err := exec.LookPath(bin); err != nil {
		if note != nil {
			note("Kokoro usa el CLI por frase. Instalá el proceso residente según el README.")
		}
		return v.speakCLI(ctx, text, speed)
	}
	gpuLib := filepath.Join(root, gpuRelease, "lib")
	if _, err := os.Stat(filepath.Join(gpuLib, "libonnxruntime_providers_cuda.so")); err != nil {
		if note != nil {
			note("Kokoro usa CPU. CUDA opcional según el README.")
		}
	} else if !e.cudaFailed && (e.worker == nil || e.worker.provider == "cuda") {
		gpuCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		wav, err := e.attempt(gpuCtx, v, bin, "cuda", gpuLib, text, speed)
		cancel()
		if err == nil {
			if note != nil {
				note("Kokoro usa GPU (CUDA).")
			}
			return wav, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		e.cudaFailed = true
	}
	if e.cudaFailed && note != nil {
		note("Kokoro usa CPU. CUDA no pudo iniciar o generar audio. Revisá CUDA 12 y cuDNN 9 según el README.")
	}
	return e.attempt(ctx, v, bin, "cpu", "", text, speed)
}

func (e *Engine) attempt(ctx context.Context, v *Voice, bin, provider, gpuLib, text string, speed float64) ([]byte, error) {
	newWorker := e.worker == nil
	if newWorker {
		w, err := startResident(v, bin, provider, gpuLib)
		if err != nil {
			return nil, err
		}
		e.worker = w
	}
	w := e.worker
	stop := context.AfterFunc(ctx, func() { _ = w.cmd.Process.Kill() })
	var wav []byte
	var err error
	if newWorker {
		err = w.expect("ready")
	}
	if err == nil {
		wav, err = w.generate(v.sid, text, speed)
	}
	if !stop() || ctx.Err() != nil {
		e.stop()
		return nil, ctx.Err()
	}
	if err != nil {
		e.stop()
	}
	return wav, err
}

func startResident(v *Voice, bin, provider, gpuLib string) (*resident, error) {
	dir, err := os.MkdirTemp("", "sonara-kokoro-")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, v.dir, v.lang, provider, filepath.Join(dir, "speech.wav"))
	if provider == "cuda" {
		paths := []string{gpuLib}
		runtime := filepath.Join(filepath.Dir(v.dir), "cuda-runtime", "nvidia")
		for _, component := range []string{"cudnn", "cufft", "cublas", "cuda_runtime", "curand"} {
			paths = append(paths, filepath.Join(runtime, component, "lib"))
		}
		for _, path := range []string{os.Getenv("SONARA_KOKORO_CUDA_LIBS"), os.Getenv("LD_LIBRARY_PATH")} {
			if path != "" {
				paths = append(paths, path)
			}
		}
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+strings.Join(paths, ":"))
	}
	input, err := cmd.StdinPipe()
	if err == nil {
		var output io.ReadCloser
		output, err = cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
			if err == nil {
				return &resident{cmd: cmd, input: input, output: bufio.NewReader(output), dir: dir, language: v.lang, provider: provider}, nil
			}
			output.Close()
		}
		input.Close()
	}
	os.RemoveAll(dir)
	return nil, fmt.Errorf("no se pudo iniciar Kokoro residente (%s): %w", provider, err)
}

func (w *resident) expect(expected string) error {
	line, err := w.output.ReadString('\n')
	if err != nil {
		return fmt.Errorf("Kokoro residente (%s): %w", w.provider, err)
	}
	if strings.TrimSpace(line) != expected {
		return fmt.Errorf("Kokoro residente (%s) no pudo generar audio", w.provider)
	}
	return nil
}

func (w *resident) generate(sid, text string, speed float64) ([]byte, error) {
	if _, err := fmt.Fprintf(w.input, "%s %.6f %d\n%s", sid, speed, len(text), text); err != nil {
		return nil, err
	}
	if err := w.expect("ok"); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(w.dir, "speech.wav"))
}
