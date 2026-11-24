package app

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync/atomic"
	"time"

	"sonara/backend/client"
	"sonara/internal/practice"
)

const practiceVoiceBytesPerMS = 48 // 24 kHz mono PCM16

// PracticeRealtime runs the optional speech-to-speech session until stopped.
func (a *App) PracticeRealtime() error {
	if a.backend == nil {
		return fmt.Errorf("Sonara backend is not initialized")
	}
	a.SetAssistantMode("practice")
	ctx, cancel := context.WithCancel(a.requestContext())
	defer cancel()
	rec := &practiceRecording{cancel: cancel, done: make(chan struct{})}
	a.mu.Lock()
	if a.practiceRec != nil {
		a.mu.Unlock()
		return fmt.Errorf("ya se está escuchando")
	}
	a.practiceRec = rec
	language, sessionContext := a.audioLanguage, a.buildSessionContextLocked()
	generation, voiceContext := a.sessionGeneration, a.voiceContext
	a.mu.Unlock()
	if voiceContext != nil {
		stopFollowingSession := context.AfterFunc(voiceContext, cancel)
		defer stopFollowingSession()
	}
	defer func() {
		defer close(rec.done)
		a.mu.Lock()
		if a.practiceRec == rec {
			a.practiceRec = nil
		}
		a.mu.Unlock()
	}()

	switch a.backend.PracticeVoiceProvider() {
	case "azure":
		if err := a.backend.CheckAzurePractice(); err != nil {
			return err
		}
		err := a.runBatchPractice(ctx, language, generation, a.backend.TranscribeAzurePractice, a.backend.SpeakAzurePractice)
		if ctx.Err() != nil {
			return nil
		}
		return err
	case "local":
		speak, err := a.backend.OpenKokoroPractice(language)
		if err != nil {
			return err
		}
		err = a.runBatchPractice(ctx, language, generation, a.backend.TranscribePractice, speak)
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	instructions := "Sos quien entrevista para el puesto del perfil. El candidato habla primero. Presentate brevemente, hacé una sola pregunta por turno y adaptá tus repreguntas a sus respuestas."
	if language == "en" {
		instructions = "You are interviewing the candidate for the role in the profile. The candidate speaks first. Introduce yourself briefly, ask one question per turn, and follow up on their answers."
	}
	if sessionContext != "" {
		instructions += "\n\n" + sessionContext
	}
	session, err := a.backend.OpenPracticeVoice(ctx, instructions+"\n\n"+practice.InterviewLanguageInstruction(language))
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer session.Close()
	ready := time.NewTimer(10 * time.Second)
	defer ready.Stop()
	select {
	case event, ok := <-session.Events():
		if ctx.Err() != nil {
			return nil
		}
		if event.Err != nil {
			return event.Err
		}
		if !ok || event.Type != "session.updated" {
			return fmt.Errorf("OpenAI no confirmó la configuración de voz a voz")
		}
	case <-ready.C:
		return fmt.Errorf("OpenAI no confirmó la sesión de voz a tiempo")
	case <-ctx.Done():
		return nil
	}

	probeCtx, cancelProbe := context.WithTimeout(ctx, 5*time.Second)
	target, err := a.practiceMicrophone(probeCtx)
	cancelProbe()
	if err != nil {
		return err
	}
	target = a.echoCanceller.Source(ctx, target)
	defer a.echoCanceller.Stop()
	capture := exec.CommandContext(ctx, "pw-record", "--target", target, "--latency", "40ms", "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	mic, err := capture.StdoutPipe()
	if err != nil {
		return err
	}
	if err := capture.Start(); err != nil {
		return fmt.Errorf("no se pudo iniciar el micrófono: %w", err)
	}
	defer func() {
		cancel()
		session.Close()
		capture.Wait()
	}()
	captureDone := make(chan error, 1)
	go func() {
		reader := practice.NewLevelReader(mic, func(level float64) { a.emit("practice:level", level) })
		pcm := make([]byte, 1280) // 40 ms at 16 kHz
		for {
			if _, err := io.ReadFull(reader, pcm); err != nil {
				captureDone <- fmt.Errorf("se cortó la captura de voz: %w", err)
				return
			}
			if err := session.Append(pcm); err != nil {
				captureDone <- fmt.Errorf("no se pudo enviar audio a OpenAI: %w", err)
				return
			}
		}
	}()

	logf("practice realtime: session ready (language=%s)", language)
	a.emit("practice:phase", "listening")
	var player *practiceVoicePlayer
	var playbackDone <-chan error
	var responseID, discardedResponse, discardedItem string
	var audioQueue practiceVoiceAudioQueue
	var responsePending bool
	var drainedResponse string
	var endpointAt time.Time
	transcripts := practiceVoiceTranscripts{app: a, ctx: ctx, language: language, generation: generation}
	defer transcripts.finish()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer func() {
		if player != nil {
			player.cancel()
			<-player.done
		}
	}()
	handleAudio := func(event client.VoiceEvent) error {
		if event.Type == "response.output_audio.done" {
			if player != nil && !player.finished {
				player.finished = true
				close(player.audio)
			}
			return nil
		}
		if player == nil {
			if !endpointAt.IsZero() {
				logf("practice realtime: endpoint to first audio chunk=%s", time.Since(endpointAt).Round(time.Millisecond))
				endpointAt = time.Time{}
			}
			var err error
			player, err = startPracticeVoicePlayer(ctx, event.ResponseID, event.ItemID, event.ContentIndex)
			if err != nil {
				return err
			}
			playbackDone = player.done
			a.emit("practice:phase", "speaking")
		}
		if player.finished {
			return nil // Late or duplicated audio for a part already closed.
		}
		select {
		case player.audio <- event.Audio:
			return nil
		default:
			return fmt.Errorf("la salida de voz superó el búfer de reproducción")
		}
	}
	finishPlayback := func(err error) error {
		finished := player
		player, playbackDone = nil, nil
		if err != nil {
			return fmt.Errorf("falló la reproducción de voz: %w", err)
		}
		if err := audioQueue.drain(func(event client.VoiceEvent) bool {
			return player == nil || player.matches(event)
		}, handleAudio); err != nil {
			return err
		}
		if player == nil || player.responseID != finished.responseID {
			if responsePending && finished.responseID == responseID {
				drainedResponse = finished.responseID
			} else {
				transcripts.played(finished.responseID)
			}
		}
		return nil
	}
	var control practiceVoiceControl
	submitAnswer := func() error {
		wasHolding := control.holding
		inputID, answer, err := control.drain(time.Now(), !responsePending && player == nil, session.DeleteInput)
		if err != nil {
			return err
		}
		if control.holding != wasHolding {
			a.emit("practice:voice-hold", control.holding)
		}
		if inputID != "" {
			transcripts.receive(client.VoiceEvent{Type: "input_audio_buffer.committed", ItemID: inputID})
			transcripts.receive(client.VoiceEvent{Type: "conversation.item.input_audio_transcription.completed", ItemID: inputID, Text: answer})
			if err := session.Respond(); err != nil {
				return err
			}
			responsePending = true
			a.emit("practice:phase", "thinking")
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-playbackDone:
				_ = finishPlayback(err)
			default:
			}
			return nil
		case <-ticker.C:
			transcripts.flush()
			if err := submitAnswer(); err != nil {
				return err
			}
		case err := <-captureDone:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case err := <-playbackDone:
			if err := finishPlayback(err); err != nil && ctx.Err() == nil {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			if player == nil {
				a.emit("practice:phase", "listening")
				if err := submitAnswer(); err != nil {
					return err
				}
			}
		case event, ok := <-session.Events():
			if ctx.Err() != nil {
				return nil
			}
			if !ok {
				return fmt.Errorf("se cerró la sesión de voz a voz")
			}
			if event.Err != nil {
				return event.Err
			}
			if control.receive(event) {
				if err := submitAnswer(); err != nil {
					return err
				}
				if !responsePending || control.preview() != "" {
					a.emit("practice:partial", control.preview())
				}
				continue
			}
			switch event.Type {
			case "response.created":
				responseID = event.ResponseID
			case "response.done":
				responsePending = false
				if player == nil && drainedResponse == event.ResponseID && event.Status == "completed" {
					transcripts.played(event.ResponseID)
					drainedResponse = ""
				}
			case "input_audio_buffer.speech_started":
				discardedResponse = responseID
				endpointAt = time.Time{}
				start := time.Now()
				hadQueuedAudio := len(audioQueue) > 0
				audioQueue = nil
				if hadQueuedAudio && transcripts.active != nil && transcripts.active.endedAt.IsZero() {
					transcripts.interrupt(transcripts.active)
				}
				select {
				case err := <-playbackDone:
					if err := finishPlayback(err); err != nil {
						return err
					}
				default:
				}
				if player != nil {
					discardedItem = player.itemID
					player.cancel()
					playedMS := player.playedMS(start)
					if err := session.Truncate(player.itemID, player.contentIndex, playedMS); err != nil {
						return err
					}
					logf("practice realtime: speech interrupted playback (estimated heard=%dms)", playedMS)
					player, playbackDone = nil, nil
				}
				a.emit("practice:phase", "capturing")
			case "input_audio_buffer.speech_stopped":
				endpointAt = time.Now()
				if control.holding {
					a.emit("practice:phase", "listening")
				} else {
					a.emit("practice:phase", "transcribing")
				}
			case "response.output_audio.delta", "response.output_audio.done":
				if event.ResponseID != responseID || event.ItemID == discardedItem ||
					(discardedResponse != "" && event.ResponseID == discardedResponse) {
					continue
				}
				if event.Type == "response.output_audio.delta" && len(event.Audio) == 0 {
					continue
				}
				if player != nil && !player.matches(event) {
					if err := audioQueue.enqueue(event); err != nil {
						return err
					}
					continue
				}
				if err := handleAudio(event); err != nil {
					return err
				}

			}
			transcripts.receive(event)
			if event.Type == "response.done" {
				if err := submitAnswer(); err != nil {
					return err
				}
			}
		}
	}
}

// Realtime can finish one audio item and send the next while PipeWire is
// still draining the first. Keep the next item's deltas and done event in order.
type practiceVoiceAudioQueue []client.VoiceEvent

func (q *practiceVoiceAudioQueue) enqueue(event client.VoiceEvent) error {
	if len(*q) >= 256 {
		return fmt.Errorf("la salida de voz superó el búfer de reproducción")
	}
	*q = append(*q, event)
	return nil
}

func (q *practiceVoiceAudioQueue) drain(canPlay func(client.VoiceEvent) bool, play func(client.VoiceEvent) error) error {
	for len(*q) > 0 && canPlay((*q)[0]) {
		event := (*q)[0]
		(*q)[0] = client.VoiceEvent{}
		*q = (*q)[1:]
		if err := play(event); err != nil {
			return err
		}
	}
	return nil
}

type practiceVoicePlayer struct {
	responseID   string
	itemID       string
	contentIndex int
	audio        chan []byte
	done         chan error
	cancel       context.CancelFunc
	finished     bool
	started      atomic.Int64
	written      atomic.Int64
}

func (p *practiceVoicePlayer) matches(event client.VoiceEvent) bool {
	return p.responseID == event.ResponseID && p.itemID == event.ItemID && p.contentIndex == event.ContentIndex
}

// PipeWire buffers output internally. Until we read its actual playback clock,
// estimate elapsed media time conservatively and bound it by bytes accepted.
func (p *practiceVoicePlayer) playedMS(at time.Time) int {
	started := p.started.Load()
	if started == 0 {
		return 0
	}
	elapsedMS := (at.UnixNano()-started)/int64(time.Millisecond) - 40
	return int(max(0, min(elapsedMS, p.written.Load()/practiceVoiceBytesPerMS)))
}

func startPracticeVoicePlayer(ctx context.Context, responseID, itemID string, contentIndex int) (*practiceVoicePlayer, error) {
	ctx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(ctx, "pw-play", "--latency", "40ms", "--raw", "--rate", "24000", "--channels", "1", "--format", "s16", "-")
	input, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("no se pudo iniciar la salida de audio: %w", err)
	}
	player := &practiceVoicePlayer{
		responseID: responseID, itemID: itemID, contentIndex: contentIndex,
		audio: make(chan []byte, 256), done: make(chan error, 1), cancel: cancel,
	}
	go func() {
		defer cancel()
		var writeErr error
		for writeErr == nil {
			select {
			case pcm, ok := <-player.audio:
				if ok {
					player.started.CompareAndSwap(0, time.Now().UnixNano())
					n, err := input.Write(pcm)
					player.written.Add(int64(n))
					writeErr = err
					continue
				}
			case <-ctx.Done():
			}
			break
		}
		input.Close()
		waitErr := command.Wait()
		if writeErr != nil {
			waitErr = writeErr
		}
		player.done <- waitErr
	}()
	return player, nil
}
