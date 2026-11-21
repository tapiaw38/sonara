package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
)

func TestReadPracticeAnswerDiscardsShortBurst(t *testing.T) {
	quiet := make([]byte, meetFrameBytes)
	shortBurst := make([]byte, meetFrameBytes)
	answer := make([]byte, meetFrameBytes)
	for offset := 0; offset < meetFrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(shortBurst[offset:], 1000)
		binary.LittleEndian.PutUint16(answer[offset:], 2000)
	}

	input := bytes.Repeat(shortBurst, practiceStartFrames)
	input = append(input, bytes.Repeat(quiet, practiceSilenceFrames)...)
	input = append(input, bytes.Repeat(answer, practiceVoiceFrames)...)
	input = append(input, bytes.Repeat(quiet, practiceSilenceFrames)...)

	detected := 0
	got, err := readPracticeAnswer(context.Background(), bytes.NewReader(input), func() bool { return false }, nil, func([]byte) { detected++ })
	if detected != 1 {
		t.Fatalf("detected %d answers, want 1", detected)
	}
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat(quiet, practiceLeadFrames)
	want = append(want, bytes.Repeat(answer, practiceVoiceFrames)...)
	want = append(want, bytes.Repeat(quiet, practiceSilenceFrames)...)
	want = trimTrailingSilence(want, practiceSilenceFrames)
	if !bytes.Equal(got, want) {
		t.Fatalf("captured %d frames, want %d frames starting at the answer", len(got)/meetFrameBytes, len(want)/meetFrameBytes)
	}
}

func TestPracticeMicrophoneRemembersFirstAvailable(t *testing.T) {
	a, database := testMeetingApp(t)
	probes := 0
	a.microphonesHook = func(context.Context) ([]AudioSource, error) {
		probes++
		return []AudioSource{{Target: "42", App: "alsa_input.mic"}}, nil
	}
	for i := 0; i < 2; i++ {
		got, err := a.practiceMicrophone(context.Background())
		if err != nil || got != "alsa_input.mic" {
			t.Fatalf("practiceMicrophone = %q, %v", got, err)
		}
	}
	if probes != 1 || a.SelectedMicrophone() != "alsa_input.mic" || database.setting(settingMicrophone) != "alsa_input.mic" {
		t.Fatal("expected one probe and a persisted microphone selection")
	}
}

func TestPracticeMicrophoneRequiresAvailableDevice(t *testing.T) {
	a, _ := testMeetingApp(t)
	a.microphonesHook = func(context.Context) ([]AudioSource, error) {
		return nil, nil
	}
	if _, err := a.practiceMicrophone(context.Background()); err == nil {
		t.Fatal("expected an error when there are no microphones")
	}
}

func TestReadPracticeAnswerInterruptsAfterSustainedVoice(t *testing.T) {
	quiet := make([]byte, meetFrameBytes)
	echo := make([]byte, meetFrameBytes)
	voice := make([]byte, meetFrameBytes)
	for offset := 0; offset < meetFrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(echo[offset:], 750)
		binary.LittleEndian.PutUint16(voice[offset:], 3500)
	}
	input := bytes.Repeat(echo, 10)
	input = append(input, bytes.Repeat(voice, practiceInterruptFrames-1)...)
	input = append(input, bytes.Repeat(echo, 4)...)
	input = append(input, bytes.Repeat(voice, practiceInterruptFrames)...)
	input = append(input, bytes.Repeat(quiet, practiceSilenceFrames)...)

	reader := bytes.NewReader(input)
	playing := true
	got, err := readPracticeAnswer(context.Background(), reader, func() bool { return playing }, nil, func([]byte) {
		if reader.Len() != practiceSilenceFrames*meetFrameBytes {
			t.Fatal("playback stopped before sustained speech")
		}
		playing = false
	})
	if err != nil {
		t.Fatal(err)
	}
	want := append(bytes.Repeat(voice, practiceInterruptFrames), bytes.Repeat(quiet, practiceSilenceFrames)...)
	if playing || !bytes.Equal(got, want) {
		t.Fatal("expected interruption audio to be captured without the preceding echo")
	}
}

func TestFramePracticeAnswerPairsQuestion(t *testing.T) {
	history := []sessionMessage{{role: "assistant", text: "¿Qué stack usás?"}, {role: "user", text: "old"}}
	got := framePracticeAnswer(lastInterviewerQuestion(history), "Go y Postgres", "es")
	if got != "Tu pregunta anterior: ¿Qué stack usás?\nRespuesta del candidato: Go y Postgres" {
		t.Fatalf("got %q", got)
	}
	if framePracticeAnswer("", "hola", "es") != "hola" {
		t.Fatal("without a previous question the answer goes as is")
	}
}
