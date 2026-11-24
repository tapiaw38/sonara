package app

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"sonara/backend/client"
)

const practiceTranscriptGrace = 5 * time.Second

type practiceVoiceTurn struct {
	id           uint64
	inputID      string
	responseID   string
	answer       string
	question     string
	answerDone   bool
	questionDone bool
	helpStarted  bool
	endedAt      time.Time
	interrupted  atomic.Bool
}

// Transcripts can arrive after playback or out of order across input items.
// Keep each exchange attached to its item/response IDs and persist in order.
type practiceVoiceTranscripts struct {
	app        *App
	ctx        context.Context
	language   string
	generation uint64
	turns      []*practiceVoiceTurn
	active     *practiceVoiceTurn
	inputID    string
}

func (s *practiceVoiceTranscripts) receive(event client.VoiceEvent) {
	switch event.Type {
	case "input_audio_buffer.speech_started":
		s.inputID = event.ItemID
		if s.active != nil && s.active.endedAt.IsZero() {
			s.interrupt(s.active)
		}
	case "input_audio_buffer.committed":
		if event.ItemID == "" || s.findInput(event.ItemID) != nil {
			return
		}
		if len(s.turns) > 0 {
			previous := s.turns[len(s.turns)-1]
			if previous.responseID == "" && previous.endedAt.IsZero() {
				s.interrupt(previous)
			}
		}
		id := s.app.nextPracticeAnswer()
		s.turns = append(s.turns, &practiceVoiceTurn{id: id, inputID: event.ItemID})
		s.inputID = event.ItemID
	case "response.created":
		if turn := s.findInput(s.inputID); turn != nil && turn.endedAt.IsZero() {
			turn.responseID = event.ResponseID
			s.active = turn
		}
	case "conversation.item.input_audio_transcription.delta", "conversation.item.input_audio_transcription.completed",
		"conversation.item.input_audio_transcription.failed":
		turn := s.findInput(event.ItemID)
		if turn == nil || turn.answerDone {
			return
		}
		switch event.Type {
		case "conversation.item.input_audio_transcription.delta":
			turn.answer += event.Text
		case "conversation.item.input_audio_transcription.completed":
			turn.answer, turn.answerDone = strings.TrimSpace(event.Text), true
		default:
			turn.answer, turn.answerDone = "", true
			logf("practice realtime: input transcription failed for turn %d", turn.id)
		}
		if event.ItemID == s.inputID {
			s.app.emit("practice:partial", turn.answer)
		}
		s.startHelp(turn)
	case "response.output_audio_transcript.delta", "response.output_audio_transcript.done":
		turn := s.findResponse(event.ResponseID)
		if turn == nil || turn.questionDone {
			return
		}
		if event.Type == "response.output_audio_transcript.delta" {
			turn.question += event.Text
			s.app.emit("practice:question", partialPracticeText(turn.id, turn.question, s.language))
		} else {
			turn.question, turn.questionDone = strings.TrimSpace(event.Text), true
			if turn.question != "" {
				s.app.publishPracticeTextWhen(s.ctx, turn.id, "practice:question", turn.question, s.language, s.current(turn))
			}
			s.startHelp(turn)
		}
	case "response.done":
		turn := s.findResponse(event.ResponseID)
		if turn != nil && (event.Status == "cancelled" || event.Status == "failed" || event.Status == "incomplete") {
			if turn.endedAt.IsZero() {
				s.interrupt(turn)
			}
			s.closeQuestion(turn)
		}
	}
	s.flush()
}

func (s *practiceVoiceTranscripts) findInput(id string) *practiceVoiceTurn {
	for _, turn := range s.turns {
		if turn.inputID == id {
			return turn
		}
	}
	return nil
}

func (s *practiceVoiceTranscripts) findResponse(id string) *practiceVoiceTurn {
	if id != "" {
		for _, turn := range s.turns {
			if turn.responseID == id {
				return turn
			}
		}
	}
	return nil
}

func (s *practiceVoiceTranscripts) startHelp(turn *practiceVoiceTurn) {
	if s.ctx.Err() != nil || turn.helpStarted || !turn.questionDone || turn.question == "" {
		return
	}
	if !turn.answerDone && (turn.endedAt.IsZero() || time.Since(turn.endedAt) < practiceTranscriptGrace) {
		return
	}
	turn.helpStarted = true
	s.app.mu.RLock()
	sessionContext := s.app.buildSessionContextLocked()
	s.app.mu.RUnlock()
	go s.app.requestPracticeSuggestionWhen(s.ctx, turn.id, s.language, turn.answer, turn.question, sessionContext, s.current(turn))
}

// current gates the turn's text. Interrupting stops the voice only: the
// question transcript, the suggested answer and translations still complete.
func (s *practiceVoiceTranscripts) current(_ *practiceVoiceTurn) func() bool {
	return func() bool {
		return s.ctx.Err() == nil
	}
}

func (s *practiceVoiceTranscripts) interrupt(turn *practiceVoiceTurn) {
	turn.interrupted.Store(true)
	turn.endedAt = time.Now()
	s.app.emit("practice:interrupted", turn.id)
}

// closeQuestion publishes whatever transcript a cut-short response reached,
// for when the server ends it before its transcript is done.
func (s *practiceVoiceTranscripts) closeQuestion(turn *practiceVoiceTurn) {
	if question := strings.TrimSpace(turn.question); question != "" && !turn.questionDone {
		turn.question, turn.questionDone = question, true
		s.app.publishPracticeTextWhen(s.ctx, turn.id, "practice:question", question, s.language, s.current(turn))
		s.startHelp(turn)
	}
}

func (s *practiceVoiceTranscripts) played(responseID string) {
	if turn := s.findResponse(responseID); turn != nil && turn.endedAt.IsZero() {
		turn.endedAt = time.Now()
		s.startHelp(turn)
	}
	s.flush()
}

func (s *practiceVoiceTranscripts) finish() {
	for _, turn := range s.turns {
		if turn.endedAt.IsZero() {
			s.interrupt(turn)
		}
		turn.endedAt = time.Now().Add(-practiceTranscriptGrace)
	}
	s.flush()
}

func (s *practiceVoiceTranscripts) flush() {
	for len(s.turns) > 0 {
		turn := s.turns[0]
		ready := turn.answerDone && (turn.questionDone || turn.interrupted.Load())
		if turn.endedAt.IsZero() || (!ready && time.Since(turn.endedAt) < practiceTranscriptGrace) {
			return
		}
		if s.ctx.Err() == nil {
			s.startHelp(turn)
		}
		s.turns[0] = nil
		s.turns = s.turns[1:]
		s.app.mu.Lock()
		if s.app.sessionGeneration == s.generation {
			if turn.answerDone && turn.answer != "" {
				s.app.appendSessionMessageLocked("user", turn.answer)
			}
			if turn.questionDone && turn.question != "" && !turn.interrupted.Load() {
				s.app.appendSessionMessageLocked("assistant", turn.question)
			}
		}
		s.app.mu.Unlock()
	}
}
