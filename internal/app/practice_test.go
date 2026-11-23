package app

import (
	"context"
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
