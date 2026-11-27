package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionPerspective(t *testing.T) {
	var old SessionProfile
	if err := json.Unmarshal([]byte(`{"scenario":"General"}`), &old); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeSessionProfile(old).Perspective; got != "candidate" {
		t.Fatal(got)
	}
	encoded, err := json.Marshal(NormalizeSessionProfile(SessionProfile{Perspective: "interviewer"}))
	if err != nil {
		t.Fatal(err)
	}
	var restored SessionProfile
	if err := json.Unmarshal(encoded, &restored); err != nil || restored.Perspective != "interviewer" {
		t.Fatal(restored, err)
	}
	for _, input := range []string{"", "unknown"} {
		if got := NormalizeSessionProfile(SessionProfile{Perspective: input}).Perspective; got != "candidate" {
			t.Fatal(got)
		}
	}
	profile := NormalizeSessionProfile(SessionProfile{Perspective: "interviewer", Notes: "liderazgo"})
	if profile.Perspective != "interviewer" {
		t.Fatal(profile)
	}
	for _, text := range []string{"User is the interviewer", "Candidate CV and competencies to assess: liderazgo"} {
		if !strings.Contains(profile.PromptContext(), text) {
			t.Fatal(text)
		}
	}
	if !strings.Contains((SessionProfile{Notes: "mi experiencia"}).PromptContext(), "User is the candidate") {
		t.Fatal("candidate missing")
	}
	if strings.Contains((SessionProfile{Notes: "mi experiencia"}).PromptContext(false), "User is the candidate") {
		t.Fatal("practice changed")
	}
}
