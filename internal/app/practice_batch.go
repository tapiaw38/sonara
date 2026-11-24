package app

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"sonara/backend/client"
	"sonara/internal/audio"
	"sonara/internal/pipewire"
	"sonara/internal/practice"
)

// Batch Practice shares turn-taking, playback and coaching across TTS providers.
func (a *App) runBatchPractice(
	ctx context.Context, language string, generation uint64,
	transcribe func(context.Context, []byte, string) (client.Transcript, error),
	speak func(context.Context, string) ([]byte, error),
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	probeCtx, cancelProbe := context.WithTimeout(ctx, 5*time.Second)
	target, err := a.practiceMicrophone(probeCtx)
	cancelProbe()
	if err != nil {
		return err
	}
	target = a.echoCanceller.Source(ctx, target)
	defer a.echoCanceller.Stop()
	threshold := practice.InterruptFrames
	if target == pipewire.EchoCancelSource {
		threshold = practice.InterruptFramesAEC
	}
	capture := exec.CommandContext(ctx, "pw-record", "--target", target, "--latency", "40ms", "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	mic, err := capture.StdoutPipe()
	if err != nil {
		return err
	}
	if err = capture.Start(); err != nil {
		return fmt.Errorf("no se pudo iniciar el micrófono: %w", err)
	}
	defer func() { cancel(); capture.Wait() }()
	reader := practice.NewLevelReader(mic, func(level float64) { a.emit("practice:level", level) })
	vad := audio.NewSpeechDetector()

	var playback *batchPracticePlayback
	defer func() {
		if playback != nil {
			playback.cancel()
			<-playback.done
		}
	}()
	var holding bool
	var answer string
	a.emit("practice:phase", "listening")
	logf("practice batch: session ready (language=%s)", language)
	for {
		pcm, err := practice.ReadAnswer(ctx, threshold, reader, func() bool {
			return playback != nil && playback.playing.Load()
		}, vad, func([]byte) {
			if playback != nil && playback.playing.Load() && playback.interrupted.CompareAndSwap(false, true) {
				playback.cancel()
				a.emit("practice:interrupted", playback.id)
			}
			a.emit("practice:phase", "capturing")
		})
		if err != nil {
			if playback != nil {
				select {
				case playErr := <-playback.done:
					playback = nil
					if playErr != nil && ctx.Err() != nil {
						return fmt.Errorf("falló la reproducción de la voz de Práctica: %w", playErr)
					}
				default:
				}
			}
			return err
		}
		if playback != nil {
			previous := playback
			playback = nil
			played := <-previous.done
			if played != nil && !previous.interrupted.Load() {
				return fmt.Errorf("falló la reproducción de la voz de Práctica: %w", played)
			}
		}
		a.emit("practice:phase", "transcribing")
		out, err := transcribe(ctx, audio.WAV(pcm), language)
		if err != nil {
			return fmt.Errorf("transcripción de Práctica: %w", err)
		}
		switch spokenPracticeCommand(out.Text) {
		case practiceVoiceHold:
			holding = true
			a.emit("practice:voice-hold", true)
			logf("practice batch: voice command recognized (hold)")
		case practiceVoiceReady:
			holding = false
			a.emit("practice:voice-hold", false)
			logf("practice batch: voice command recognized (ready)")
		default:
			answer = strings.TrimSpace(answer + " " + out.Text)
		}
		a.emit("practice:partial", answer)
		if holding || answer == "" {
			a.emit("practice:phase", "listening")
			continue
		}

		a.emit("practice:phase", "thinking")
		a.mu.RLock()
		sessionContext := a.buildSessionContextLocked()
		a.mu.RUnlock()
		request := practice.InterviewLanguageInstruction(language) + "\n\n" + answer
		result, err := a.backend.Respond(ctx, request, "practice-question", sessionContext)
		if err != nil {
			return err
		}
		question := strings.TrimSpace(result.Text)
		if question == "" {
			return fmt.Errorf("no se generó una pregunta para Práctica")
		}
		a.emit("practice:phase", "synthesizing")
		wav, err := speak(ctx, question)
		if err != nil {
			return err
		}
		p, err := a.startBatchPracticePlayback(ctx, cancel, wav, generation, answer, question)
		if err != nil {
			return err
		}
		playback = p
		// Interrupting stops the voice only: the question, the suggested
		// answer and their translations still complete.
		current := func() bool { return ctx.Err() == nil }
		a.publishPracticeTextWhen(ctx, p.id, "practice:question", question, language, current)
		go a.requestPracticeSuggestionWhen(ctx, p.id, language, answer, question, sessionContext, current)
		answer = ""
	}
}

type batchPracticePlayback struct {
	id                   uint64
	cancel               context.CancelFunc
	playing, interrupted atomic.Bool
	done                 chan error
}

func (a *App) startBatchPracticePlayback(ctx context.Context, stopCapture context.CancelFunc, wav []byte, generation uint64, answer, question string) (*batchPracticePlayback, error) {
	playCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(playCtx, "pw-play", "--latency", "40ms", "-")
	cmd.Stdin = bytes.NewReader(wav)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("no se pudo reproducir la voz de Práctica: %w", err)
	}
	p := &batchPracticePlayback{id: a.nextPracticeAnswer(), cancel: cancel, done: make(chan error, 1)}
	p.playing.Store(true)
	a.emit("practice:phase", "speaking")
	go func() {
		defer cancel()
		err := cmd.Wait()
		p.playing.Store(false)
		a.mu.Lock()
		if a.sessionGeneration == generation {
			a.appendSessionMessageLocked("user", answer)
			if err == nil && !p.interrupted.Load() {
				a.appendSessionMessageLocked("assistant", question)
			}
		}
		a.mu.Unlock()
		if playCtx.Err() == nil && err == nil {
			a.emit("practice:phase", "listening")
		}
		p.done <- err
		if err != nil && playCtx.Err() == nil {
			stopCapture()
		}
	}()
	return p, nil
}
