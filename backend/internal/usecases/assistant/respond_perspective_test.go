package assistant

import (
	"strings"
	"testing"
)

func TestInterviewerRouting(t *testing.T) {
	if got := normalizeMode("interview-interviewer"); got != "interview-interviewer" {
		t.Fatal(got)
	}
	_, _, instructions := chooseAgent("un proyecto", "interview-interviewer")
	for _, phrase := range []string{"Repregunta:", "Siguiente:", "Señales:", "sin evidencia", "No inventes"} {
		if !strings.Contains(instructions, phrase) {
			t.Fatal(phrase)
		}
	}
	_, _, candidate := chooseAgent("un proyecto", "interview")
	if !strings.Contains(candidate, "lista para decir") || strings.Contains(candidate, "Repregunta:") {
		t.Fatal(candidate)
	}
}
