package app

import (
	"sonara/internal/storage"
	"strings"
	"testing"
)

func TestPracticeContextIgnoresInterviewerPerspective(t *testing.T) {
	a := &App{assistantMode: "practice", sessionProfile: storage.SessionProfile{Perspective: "interviewer", Notes: "example"}}
	context := a.buildSessionContextLocked()
	if strings.Contains(context, "User is the interviewer") || strings.Contains(context, "Candidate CV and competencies") {
		t.Fatal(context)
	}
}

func TestCopilotInterviewPerspective(t *testing.T) {
	interviewer := storage.SessionProfile{Perspective: "interviewer"}
	if got := copilotRequestMode("interview", interviewer); got != "interview-interviewer" {
		t.Fatal(got)
	}
	for _, mode := range []string{"daily", "coding", "coaching", "practice"} {
		if got := copilotRequestMode(mode, interviewer); got != mode {
			t.Fatal(mode, got)
		}
	}
	if got := copilotRequestMode("interview", storage.SessionProfile{}); got != "interview" {
		t.Fatal(got)
	}
	if got := voicePrompt("en", "hello", "candidate"); got != voicePrompt("en", "hello") {
		t.Fatal("candidate prompt changed")
	}
	prompt := voicePrompt("es", "experiencia", "interviewer")
	for _, text := range []string{"Repregunta:", "Siguiente:", "Señales:", "sin evidencia"} {
		if !strings.Contains(prompt, text) {
			t.Fatal(text)
		}
	}
	if got := memoryRole("interview", "me", "interviewer"); got != "me (what I asked as interviewer)" {
		t.Fatal(got)
	}
	if got := memoryRole("interview", "user", "interviewer"); got != "candidate" {
		t.Fatal(got)
	}
	if got := memoryRole("practice", "user", "interviewer"); got != "me (what I said out loud)" {
		t.Fatal(got)
	}
}
