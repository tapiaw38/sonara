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
	got, err := readPracticeAnswer(context.Background(), practiceInterruptFrames, bytes.NewReader(input), func() bool { return false }, nil, func([]byte) { detected++ })
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
	got, err := readPracticeAnswer(context.Background(), practiceInterruptFrames, reader, func() bool { return playing }, nil, func([]byte) {
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

// Natural speech dips between syllables: a dip must not reset an interruption.
func TestReadPracticeAnswerInterruptsDespiteDips(t *testing.T) {
	quiet := make([]byte, meetFrameBytes)
	echo := make([]byte, meetFrameBytes)
	voice := make([]byte, meetFrameBytes)
	for offset := 0; offset < meetFrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(echo[offset:], 750)
		binary.LittleEndian.PutUint16(voice[offset:], 3500)
	}
	input := bytes.Repeat(echo, 10)
	for range 3 { // voice, voice, dip — never 6 in a row
		input = append(input, voice...)
		input = append(input, voice...)
		input = append(input, echo...)
	}
	input = append(input, bytes.Repeat(voice, 6)...)
	input = append(input, bytes.Repeat(quiet, practiceSilenceFrames)...)
	playing, interrupted := true, false
	_, err := readPracticeAnswer(context.Background(), practiceInterruptFrames, bytes.NewReader(input), func() bool { return playing }, nil, func([]byte) {
		interrupted, playing = true, false
	})
	if err != nil || !interrupted {
		t.Fatalf("interrupted=%v err=%v; speech with dips must still cut the interviewer off", interrupted, err)
	}
}

// Talking over the interviewer for a while must not teach the echo estimate
// your own voice: the bar would climb above it and never let you cut in.
func TestReadPracticeAnswerVoiceDoesNotRaiseEchoBar(t *testing.T) {
	quiet := make([]byte, meetFrameBytes)
	echo := make([]byte, meetFrameBytes)
	voice := make([]byte, meetFrameBytes)
	for offset := 0; offset < meetFrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(echo[offset:], 750)
		binary.LittleEndian.PutUint16(voice[offset:], 3500)
	}
	input := bytes.Repeat(echo, 5)
	for range 10 { // half voice: 4/8, below the trigger, for 4 seconds
		input = append(input, voice...)
		input = append(input, voice...)
		input = append(input, echo...)
		input = append(input, echo...)
	}
	for range 3 { // then denser speech
		input = append(input, voice...)
		input = append(input, voice...)
		input = append(input, voice...)
		input = append(input, echo...)
	}
	input = append(input, bytes.Repeat(quiet, practiceSilenceFrames)...)
	playing, interrupted := true, false
	_, _ = readPracticeAnswer(context.Background(), practiceInterruptFrames, bytes.NewReader(input), func() bool { return playing }, nil, func([]byte) {
		interrupted, playing = true, false
	})
	if !interrupted {
		t.Fatal("sustained speech over the interviewer must still cut it off")
	}
}

// The interviewer's own echo, louder on some syllables, must not cut it off.
func TestReadPracticeAnswerIgnoresEchoAlone(t *testing.T) {
	soft := make([]byte, meetFrameBytes)
	loud := make([]byte, meetFrameBytes)
	for offset := 0; offset < meetFrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(soft[offset:], 750)
		binary.LittleEndian.PutUint16(loud[offset:], 1100)
	}
	var input []byte
	for range 30 {
		input = append(input, soft...)
		input = append(input, loud...)
	}
	interrupted := false
	_, _ = readPracticeAnswer(context.Background(), practiceInterruptFrames, bytes.NewReader(input), func() bool { return true }, nil, func([]byte) {
		interrupted = true
	})
	if interrupted {
		t.Fatal("echo alone cut the interviewer off")
	}
}

// Room noise above the initial floor, which the VAD sometimes takes for voice:
// the pause after the answer must still end it, not the 15s limit.
func TestReadPracticeAnswerEndsOverRoomNoise(t *testing.T) {
	noise := make([]byte, meetFrameBytes)
	voice := make([]byte, meetFrameBytes)
	for offset := 0; offset < meetFrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(noise[offset:], 300) // ~0.009, above meetNoiseFloor
		binary.LittleEndian.PutUint16(voice[offset:], 3500)
	}
	input := bytes.Repeat(noise, 10)
	input = append(input, bytes.Repeat(voice, 20)...)
	input = append(input, bytes.Repeat(noise, 100)...)
	frame := 0
	vad := func(f []byte) bool { // voice always, and every third noise frame
		frame++
		return pcmRMS(f) > 0.05 || frame%3 == 0
	}
	reader := bytes.NewReader(input)
	got, err := readPracticeAnswer(context.Background(), practiceInterruptFrames, reader, func() bool { return false }, vad, func([]byte) {})
	if err != nil {
		t.Fatal(err)
	}
	if reader.Len() == 0 {
		t.Fatal("the pause was never noticed: the answer ran until the audio ended")
	}
	if len(got) < 20*meetFrameBytes {
		t.Fatalf("answer too short: %d frames", len(got)/meetFrameBytes)
	}
}

func TestHasWords(t *testing.T) {
	for text, want := range map[string]bool{"": false, " . ... ": false, "¿?": false, "um": true, "Go 2": true, "ñ": true} {
		if hasWords(text) != want {
			t.Errorf("hasWords(%q) = %v", text, !want)
		}
	}
}
