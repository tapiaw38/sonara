package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sonara/backend/client"
	"sonara/internal/practice"
)

type practiceRecording struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StopPractice cancels the voice session and waits for its audio devices to close.
func (a *App) StopPractice() {
	a.mu.RLock()
	rec := a.practiceRec
	a.mu.RUnlock()
	if rec != nil {
		rec.cancel()
		<-rec.done
	}
}

func (a *App) nextPracticeAnswer() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.practiceAnswerID++
	return a.practiceAnswerID
}

type practiceText struct {
	TurnID      uint64                   `json:"turnId"`
	Original    string                   `json:"original"`
	Language    string                   `json:"language"`
	Translation string                   `json:"translation,omitempty"`
	Lines       []client.TranslationLine `json:"lines,omitempty"`
}

func (a *App) requestPracticeSuggestionWhen(ctx context.Context, answerID uint64, language, answer, question, sessionContext string, current func() bool) {
	request := practice.InterviewLanguageInstruction(language) + "\n\n" +
		"Lo último que dijo el candidato:\n" + answer + "\n\nÚltima pregunta del entrevistador:\n" + question
	suggestion := retryPracticeText(ctx, answerID, "suggestion", current, func(requestCtx context.Context) (string, error) {
		result, err := a.backend.Respond(requestCtx, request, "practice-suggestion", sessionContext)
		return result.Text, err
	})
	if !current() {
		return
	}
	if suggestion == "" {
		message := "No se pudo generar la respuesta sugerida."
		if language == "en" {
			message = "Could not generate a suggested answer."
		}
		a.emit("practice:suggestion", partialPracticeText(answerID, message, language))
		return
	}
	a.publishPracticeTextWhen(ctx, answerID, "practice:suggestion", suggestion, language, current)
}

func partialPracticeText(answerID uint64, original, language string) practiceText {
	return practiceText{TurnID: answerID, Original: original, Language: language}
}

func (a *App) publishPracticeTextWhen(ctx context.Context, answerID uint64, event, original, language string, current func() bool) {
	if !current() {
		return
	}
	text := partialPracticeText(answerID, original, language)
	target := "en"
	if language == "en" {
		target = "es"
	}
	a.emit(event, text)
	// The text is on screen now, so its translation always finishes: in its
	// own goroutine, detached from the turn and the session, so interrupting
	// the interviewer or ending the practice stops what comes next but never
	// leaves a line half translated. Bounded by its own timeouts.
	go func() {
		detached := context.Background()
		requestCtx, cancel := context.WithTimeout(detached, 8*time.Second)
		lines, err := a.backend.TranslateAligned(requestCtx, original, target)
		cancel()
		if err == nil {
			text.Lines = lines
		} else {
			text.Translation = retryPracticeText(detached, answerID, event+" translation", func() bool { return true }, func(requestCtx context.Context) (string, error) {
				return a.backend.TranslateTo(requestCtx, original, target)
			})
		}
		a.emit(event, text)
	}()
}

// retryPracticeText retries the same provider once. The voice session and
// playback keep running while a text request is in flight.
func retryPracticeText(ctx context.Context, turnID uint64, operation string, current func() bool, request func(context.Context) (string, error)) string {
	for attempt := 1; attempt <= 2; attempt++ {
		if ctx.Err() != nil || !current() {
			break
		}
		requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		value, err := request(requestCtx)
		cancel()
		if ctx.Err() != nil || !current() {
			break
		}
		value = strings.TrimSpace(value)
		if err == nil && value != "" {
			return value
		}
		reason := "empty response"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timeout"
		} else if err != nil {
			reason = "request failed"
		}
		logf("practice: %s turn=%d attempt=%d: %s", operation, turnID, attempt, reason)
		if attempt == 1 {
			select {
			case <-time.After(250 * time.Millisecond):
			case <-ctx.Done():
				return ""
			}
		}
	}
	return ""
}

// practiceMicrophone returns a stable PipeWire node name that pw-record can target.
func (a *App) practiceMicrophone(ctx context.Context) (string, error) {
	if selected := a.SelectedMicrophone(); selected != "" {
		return selected, nil
	}
	sources, err := a.microphones(ctx)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preferredMic != "" {
		return a.preferredMic, nil
	}
	for _, source := range sources {
		if source.App != "" {
			a.setMicrophoneLocked(source.App)
			return source.App, nil
		}
	}
	return "", fmt.Errorf("no hay micrófono disponible; revisá que PipeWire esté activo")
}
