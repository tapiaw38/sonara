package practice

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"sonara/internal/audio"
	"sonara/internal/storage"
)

func TestReadAnswerDiscardsShortBurst(t *testing.T) {
	quiet := make([]byte, audio.FrameBytes)
	shortBurst := make([]byte, audio.FrameBytes)
	answer := make([]byte, audio.FrameBytes)
	for offset := 0; offset < audio.FrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(shortBurst[offset:], 1000)
		binary.LittleEndian.PutUint16(answer[offset:], 2000)
	}

	input := bytes.Repeat(shortBurst, minStartFrames)
	input = append(input, bytes.Repeat(quiet, endSilenceFrames)...)
	input = append(input, bytes.Repeat(answer, minVoiceFrames)...)
	input = append(input, bytes.Repeat(quiet, endSilenceFrames)...)

	detected := 0
	got, err := ReadAnswer(context.Background(), InterruptFrames, bytes.NewReader(input), func() bool { return false }, nil, func([]byte) { detected++ })
	if detected != 1 {
		t.Fatalf("detected %d answers, want 1", detected)
	}
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat(quiet, leadFrames)
	want = append(want, bytes.Repeat(answer, minVoiceFrames)...)
	want = append(want, bytes.Repeat(quiet, endSilenceFrames)...)
	want = audio.TrimTrailingSilence(want, endSilenceFrames, keepSilenceFrames, minAnswerFrames)
	if !bytes.Equal(got, want) {
		t.Fatalf("captured %d frames, want %d frames starting at the answer", len(got)/audio.FrameBytes, len(want)/audio.FrameBytes)
	}
}

func TestReadAnswerInterruptsAfterSustainedVoice(t *testing.T) {
	quiet := make([]byte, audio.FrameBytes)
	echo := make([]byte, audio.FrameBytes)
	voice := make([]byte, audio.FrameBytes)
	for offset := 0; offset < audio.FrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(echo[offset:], 750)
		binary.LittleEndian.PutUint16(voice[offset:], 3500)
	}
	input := bytes.Repeat(echo, 10)
	input = append(input, bytes.Repeat(voice, InterruptFrames-1)...)
	input = append(input, bytes.Repeat(echo, 4)...)
	input = append(input, bytes.Repeat(voice, InterruptFrames)...)
	input = append(input, bytes.Repeat(quiet, endSilenceFrames)...)

	reader := bytes.NewReader(input)
	playing := true
	got, err := ReadAnswer(context.Background(), InterruptFrames, reader, func() bool { return playing }, nil, func([]byte) {
		if reader.Len() != endSilenceFrames*audio.FrameBytes {
			t.Fatal("playback stopped before sustained speech")
		}
		playing = false
	})
	if err != nil {
		t.Fatal(err)
	}
	want := append(bytes.Repeat(voice, InterruptFrames), bytes.Repeat(quiet, endSilenceFrames)...)
	if playing || !bytes.Equal(got, want) {
		t.Fatal("expected interruption audio to be captured without the preceding echo")
	}
}

func TestFrameAnswerPairsQuestion(t *testing.T) {
	history := []storage.Message{{Role: "assistant", Text: "¿Qué stack usás?"}, {Role: "user", Text: "old"}}
	got := FrameAnswer(LastInterviewerQuestion(history), "Go y Postgres", "es")
	if got != "Tu pregunta anterior: ¿Qué stack usás?\nRespuesta del candidato: Go y Postgres" {
		t.Fatalf("got %q", got)
	}
	if FrameAnswer("", "hola", "es") != "hola" {
		t.Fatal("without a previous question the answer goes as is")
	}
}

// Natural speech dips between syllables: a dip must not reset an interruption.
func TestReadAnswerInterruptsDespiteDips(t *testing.T) {
	quiet := make([]byte, audio.FrameBytes)
	echo := make([]byte, audio.FrameBytes)
	voice := make([]byte, audio.FrameBytes)
	for offset := 0; offset < audio.FrameBytes; offset += 2 {
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
	input = append(input, bytes.Repeat(quiet, endSilenceFrames)...)
	playing, interrupted := true, false
	_, err := ReadAnswer(context.Background(), InterruptFrames, bytes.NewReader(input), func() bool { return playing }, nil, func([]byte) {
		interrupted, playing = true, false
	})
	if err != nil || !interrupted {
		t.Fatalf("interrupted=%v err=%v; speech with dips must still cut the interviewer off", interrupted, err)
	}
}

// Talking over the interviewer for a while must not teach the echo estimate
// your own voice: the bar would climb above it and never let you cut in.
func TestReadAnswerVoiceDoesNotRaiseEchoBar(t *testing.T) {
	quiet := make([]byte, audio.FrameBytes)
	echo := make([]byte, audio.FrameBytes)
	voice := make([]byte, audio.FrameBytes)
	for offset := 0; offset < audio.FrameBytes; offset += 2 {
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
	input = append(input, bytes.Repeat(quiet, endSilenceFrames)...)
	playing, interrupted := true, false
	_, _ = ReadAnswer(context.Background(), InterruptFrames, bytes.NewReader(input), func() bool { return playing }, nil, func([]byte) {
		interrupted, playing = true, false
	})
	if !interrupted {
		t.Fatal("sustained speech over the interviewer must still cut it off")
	}
}

// The interviewer's own echo, louder on some syllables, must not cut it off.
func TestReadAnswerIgnoresEchoAlone(t *testing.T) {
	soft := make([]byte, audio.FrameBytes)
	loud := make([]byte, audio.FrameBytes)
	for offset := 0; offset < audio.FrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(soft[offset:], 750)
		binary.LittleEndian.PutUint16(loud[offset:], 1100)
	}
	var input []byte
	for range 30 {
		input = append(input, soft...)
		input = append(input, loud...)
	}
	interrupted := false
	_, _ = ReadAnswer(context.Background(), InterruptFrames, bytes.NewReader(input), func() bool { return true }, nil, func([]byte) {
		interrupted = true
	})
	if interrupted {
		t.Fatal("echo alone cut the interviewer off")
	}
}

// Room noise above the initial floor, which the VAD sometimes takes for voice:
// the pause after the answer must still end it, not the 15s limit.
func TestReadAnswerEndsOverRoomNoise(t *testing.T) {
	noise := make([]byte, audio.FrameBytes)
	voice := make([]byte, audio.FrameBytes)
	for offset := 0; offset < audio.FrameBytes; offset += 2 {
		binary.LittleEndian.PutUint16(noise[offset:], 300) // ~0.009, above meetNoiseFloor
		binary.LittleEndian.PutUint16(voice[offset:], 3500)
	}
	input := bytes.Repeat(noise, 10)
	input = append(input, bytes.Repeat(voice, 20)...)
	input = append(input, bytes.Repeat(noise, 100)...)
	frame := 0
	vad := func(f []byte) bool { // voice always, and every third noise frame
		frame++
		return audio.RMS(f) > 0.05 || frame%3 == 0
	}
	reader := bytes.NewReader(input)
	got, err := ReadAnswer(context.Background(), InterruptFrames, reader, func() bool { return false }, vad, func([]byte) {})
	if err != nil {
		t.Fatal(err)
	}
	if reader.Len() == 0 {
		t.Fatal("the pause was never noticed: the answer ran until the audio ended")
	}
	if len(got) < 20*audio.FrameBytes {
		t.Fatalf("answer too short: %d frames", len(got)/audio.FrameBytes)
	}
}

func TestHasWords(t *testing.T) {
	for text, want := range map[string]bool{"": false, " . ... ": false, "¿?": false, "um": true, "Go 2": true, "ñ": true} {
		if HasWords(text) != want {
			t.Errorf("hasWords(%q) = %v", text, !want)
		}
	}
}
