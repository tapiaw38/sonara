package app

import (
	"context"
	"errors"
	"testing"

	"sonara/internal/pipewire"
	"sonara/internal/storage"
)

func TestPracticeMicrophoneRemembersFirstAvailable(t *testing.T) {
	a, database := testMeetingApp(t)
	probes := 0
	a.microphonesHook = func(context.Context) ([]pipewire.Source, error) {
		probes++
		return []pipewire.Source{{Target: "42", App: "alsa_input.mic"}}, nil
	}
	for i := 0; i < 2; i++ {
		got, err := a.practiceMicrophone(context.Background())
		if err != nil || got != "alsa_input.mic" {
			t.Fatalf("practiceMicrophone = %q, %v", got, err)
		}
	}
	if probes != 1 || a.SelectedMicrophone() != "alsa_input.mic" || database.Setting(storage.SettingMicrophone) != "alsa_input.mic" {
		t.Fatal("expected one probe and a persisted microphone selection")
	}
}

func TestPracticeMicrophoneRequiresAvailableDevice(t *testing.T) {
	a, _ := testMeetingApp(t)
	a.microphonesHook = func(context.Context) ([]pipewire.Source, error) {
		return nil, nil
	}
	if _, err := a.practiceMicrophone(context.Background()); err == nil {
		t.Fatal("expected an error when there are no microphones")
	}
}

func TestPracticeTextRecoversFromFirstRequestFailure(t *testing.T) {
	attempts := 0
	got := retryPracticeText(context.Background(), 1, "translation", func() bool { return true }, func(context.Context) (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("first request failed")
		}
		return " Hola ", nil
	})
	if got != "Hola" || attempts != 2 {
		t.Fatalf("got %q after %d attempts, want Hola after 2", got, attempts)
	}
}

func TestPracticeTextDoesNotRetryInterruptedTurn(t *testing.T) {
	attempts := 0
	got := retryPracticeText(context.Background(), 1, "translation", func() bool { return attempts == 0 }, func(context.Context) (string, error) {
		attempts++
		return "", errors.New("turn interrupted")
	})
	if got != "" || attempts != 1 {
		t.Fatalf("got %q after %d attempts, want no text after 1", got, attempts)
	}
}
