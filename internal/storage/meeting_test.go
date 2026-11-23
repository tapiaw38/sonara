package storage

import "testing"

func TestParticipantLabel(t *testing.T) {
	if ParticipantLabel(TrackMicrophone, 1) != "Participante B" || ParticipantLabel(TrackOutput, 1) != "Participante 2" {
		t.Fatal("microphone people are lettered, call people numbered")
	}
}
