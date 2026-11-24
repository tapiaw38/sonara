package app

import (
	"math"
	"testing"
)

func TestPracticeVoiceSpeedDefaultsAndPersists(t *testing.T) {
	a, database := testMeetingApp(t)
	if a.practiceVoiceSpeed("local") != 0.85 || a.practiceVoiceSpeed("openai") != 1 || a.practiceVoiceSpeed("azure") != 1 {
		t.Fatal("unexpected defaults")
	}
	for _, edge := range []float64{0.5, 1.5} {
		if err := a.SetPracticeVoiceSpeed(edge); err != nil {
			t.Fatalf("rejected boundary %v: %v", edge, err)
		}
	}
	if err := a.SetPracticeVoiceSpeed(1.15); err != nil {
		t.Fatal(err)
	}
	reopened := &App{store: database}
	for _, speed := range []float64{0.49, 1.51, math.NaN()} {
		if err := reopened.SetPracticeVoiceSpeed(speed); err == nil {
			t.Fatalf("accepted %v", speed)
		}
	}
	if got := reopened.practiceVoiceSpeed("local"); got != 1.15 {
		t.Fatalf("persisted speed = %v", got)
	}
}
