package audio

import (
	webrtcvad "github.com/maxhawkins/go-webrtcvad"

	"sonara/internal/logx"
)

// vadDetector asks WebRTC's voice detector whether a frame sounds like a
// person, on top of the loudness checks: a fan, a door, music or a hissy mic
// are loud enough to pass an energy threshold but are not speech. Same rule as
// gillie's listen mode: mode 2 (balanced), speech when at least half of the
// 10ms pieces of a 100ms frame are.
//
// One per capture loop: the detector keeps state between frames.
type vadDetector struct {
	vad *webrtcvad.VAD
}

const (
	vadMode      = 2
	vadPieceSize = SampleRate / 100 * 2 // 10ms of 16-bit mono
)

// NewSpeechDetector never fails the caller: without a detector every loud
// frame counts as speech, exactly as before it existed.
func NewSpeechDetector() func([]byte) bool {
	vad, err := webrtcvad.New()
	if err != nil || vad.SetMode(vadMode) != nil {
		logx.Dedup("speech-vad", "voice detector unavailable, using loudness only: %v", err)
		return nil
	}
	return (&vadDetector{vad: vad}).isSpeech
}

func (d *vadDetector) isSpeech(frame []byte) bool {
	pieces, speech := 0, 0
	for start := 0; start+vadPieceSize <= len(frame); start += vadPieceSize {
		active, err := d.vad.Process(SampleRate, frame[start:start+vadPieceSize])
		if err != nil {
			return true
		}
		pieces++
		if active {
			speech++
		}
	}
	return pieces == 0 || speech*2 >= pieces
}

// IsVoice combines both checks; a nil detector means loudness only.
func IsVoice(detector func([]byte) bool, frame []byte, loud bool) bool {
	return loud && (detector == nil || detector(frame))
}
