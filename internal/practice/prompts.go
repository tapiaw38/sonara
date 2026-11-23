package practice

import (
	"strings"
	"unicode"

	"sonara/internal/storage"
)

func RetryPrompt(language string) string {
	if language == "en" {
		return "I didn't catch that. Could you repeat your answer?"
	}
	return "No te escuché bien. ¿Podés repetir tu respuesta?"
}

// HasWords reports whether a transcript holds any letter or digit, not just
// punctuation the transcriber wrote for a noise.
func HasWords(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// LastInterviewerQuestion is the question the candidate was just answering.
func LastInterviewerQuestion(history []storage.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			return history[i].Text
		}
	}
	return ""
}

// FrameAnswer pairs the answer with the question it answers, so the
// next question can build on both instead of on a bare sentence.
func FrameAnswer(question, answer, language string) string {
	if question == "" {
		return answer
	}
	if language == "en" {
		return "Your previous question: " + question + "\nCandidate's answer: " + answer
	}
	return "Tu pregunta anterior: " + question + "\nRespuesta del candidato: " + answer
}
