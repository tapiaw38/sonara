package app

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	TurnID  uint64 `json:"turnId"`
	English string `json:"en"`
	Spanish string `json:"es"`
}

func (a *App) requestPracticeSuggestionWhen(ctx context.Context, answerID uint64, language, answer, question, sessionContext string, current func() bool) {
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request := practice.InterviewLanguageInstruction(language) + "\n\n" +
		"Lo último que dijo el candidato:\n" + answer + "\n\nÚltima pregunta del entrevistador:\n" + question
	result, err := a.backend.Respond(requestCtx, request, "practice-suggestion", sessionContext)
	if !current() {
		return
	}
	suggestion := strings.TrimSpace(result.Text)
	if err != nil || suggestion == "" {
		logf("practice: could not generate suggestion for turn %d", answerID)
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
	if language == "en" {
		return practiceText{TurnID: answerID, English: original}
	}
	return practiceText{TurnID: answerID, Spanish: original}
}

func (a *App) publishPracticeTextWhen(ctx context.Context, answerID uint64, event, original, language string, current func() bool) {
	if !current() {
		return
	}
	text := practiceText{TurnID: answerID, Spanish: original}
	target := "en"
	if language == "en" {
		text = practiceText{TurnID: answerID, English: original}
		target = "es"
	}
	a.emit(event, text)
	go func() {
		translationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		translated, err := a.backend.TranslateTo(translationCtx, original, target)
		translated = strings.TrimSpace(translated)
		if err != nil || translated == "" {
			translated = "Traducción no disponible."
		}
		if target == "es" {
			text.Spanish = translated
		} else {
			text.English = translated
		}
		if current() {
			a.emit(event, text)
		}
	}()
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
