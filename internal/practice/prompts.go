package practice

// InterviewLanguageInstruction keeps the configured interview language even
// when the candidate or the existing session context uses another language.
func InterviewLanguageInstruction(language string) string {
	if language == "en" {
		return "Interview language: English. Use English throughout, even when the candidate or earlier conversation uses another language."
	}
	return "Idioma de la entrevista: español. Usá español durante toda la entrevista, aunque el candidato o el historial usen otro idioma."
}
