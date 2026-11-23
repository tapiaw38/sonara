package app

import (
	"strings"
	"time"
	"unicode"

	"sonara/backend/client"
)

type practiceVoiceInput struct {
	id       string
	text     string
	deadline time.Time
	done     bool
	failed   bool
}

type practiceVoiceControl struct {
	holding     bool
	speaking    bool
	awaitCommit bool
	waiting     []practiceVoiceInput
	answerID    string
	answer      string
}

// receive holds committed items in order because ASR can finish a later item
// first. The conversation retains answer audio across pauses.
func (c *practiceVoiceControl) receive(event client.VoiceEvent) bool {
	switch event.Type {
	case "input_audio_buffer.speech_started":
		c.speaking = true
	case "input_audio_buffer.speech_stopped":
		c.speaking, c.awaitCommit = false, true
	case "input_audio_buffer.committed":
		c.speaking, c.awaitCommit = false, false
		if event.ItemID != "" {
			c.waiting = append(c.waiting, practiceVoiceInput{id: event.ItemID, deadline: time.Now().Add(practiceTranscriptGrace)})
		}
		return true
	case "conversation.item.input_audio_transcription.delta",
		"conversation.item.input_audio_transcription.completed", "conversation.item.input_audio_transcription.failed":
		for i := range c.waiting {
			input := &c.waiting[i]
			if input.id != event.ItemID || input.done {
				continue
			}
			switch event.Type {
			case "conversation.item.input_audio_transcription.delta":
				input.text += event.Text
			case "conversation.item.input_audio_transcription.completed":
				input.text, input.done = strings.TrimSpace(event.Text), true
			default:
				input.text, input.done, input.failed = "", true, true
			}
			break
		}
		return true
	}
	return false
}

type practiceVoiceCommand int

const (
	practiceVoiceNoCommand practiceVoiceCommand = iota
	practiceVoiceHold
	practiceVoiceReady
)

func spokenPracticeCommand(text string) practiceVoiceCommand {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	switch strings.Join(words, " ") {
	case "esperá", "espera", "escuchá", "escucha", "un momento", "wait", "hold on", "listen":
		return practiceVoiceHold
	case "listo", "lista", "ya terminé", "ya termine", "puedes responder", "podés responder",
		"done", "i m done", "i am done", "ready", "you can answer now":
		return practiceVoiceReady
	default:
		return practiceVoiceNoCommand
	}
}

// drain returns a single submitted answer once speech and command recognition
// finish. Holding accumulates segments without requesting a response.
func (c *practiceVoiceControl) drain(now time.Time, canRespond bool, deleteInput func(string) error) (id, answer string, err error) {
	for len(c.waiting) > 0 {
		input := c.waiting[0]
		if !input.done && now.Before(input.deadline) {
			return "", "", nil
		}
		c.waiting = c.waiting[1:]
		if input.done {
			command := spokenPracticeCommand(input.text)
			if command != practiceVoiceNoCommand {
				if err := deleteInput(input.id); err != nil {
					return "", "", err
				}
				c.holding = command == practiceVoiceHold
				label := "ready"
				if c.holding {
					label = "hold"
				}
				logf("practice realtime: voice command recognized (%s)", label)
				continue
			}
		}
		// Empty, successful transcriptions are usually noise. Keep real audio
		// available to the voice model even if the transcription failed.
		if input.text == "" && input.done && !input.failed {
			if err := deleteInput(input.id); err != nil {
				return "", "", err
			}
			continue
		}
		c.answerID = input.id
		c.answer = strings.TrimSpace(c.answer + " " + input.text)
	}
	if c.holding || c.speaking || c.awaitCommit || !canRespond {
		return "", "", nil
	}
	id, answer = c.answerID, c.answer
	c.answerID, c.answer = "", ""
	return id, answer, nil
}

func (c *practiceVoiceControl) preview() string {
	parts := []string{c.answer}
	for _, pending := range c.waiting {
		if spokenPracticeCommand(pending.text) == practiceVoiceNoCommand {
			parts = append(parts, pending.text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
