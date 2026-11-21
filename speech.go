package main

import (
	webrtcvad "github.com/maxhawkins/go-webrtcvad"
)

// speechDetector asks WebRTC's voice detector whether a frame sounds like a
// person, on top of the loudness checks: a fan, a door, music or a hissy mic
// are loud enough to pass an energy threshold but are not speech. Same rule as
// gillie's listen mode: mode 2 (balanced), speech when at least half of the
// 10ms pieces of a 100ms frame are.
//
// One per capture loop: the detector keeps state between frames.
type speechDetector struct {
	vad *webrtcvad.VAD
}

const (
	speechVADMode   = 2
	speechPieceSize = meetSampleRate / 100 * 2 // 10ms of 16-bit mono
)

// newSpeechDetector never fails the caller: without a detector every loud
// frame counts as speech, exactly as before it existed.
func newSpeechDetector() func([]byte) bool {
	vad, err := webrtcvad.New()
	if err != nil || vad.SetMode(speechVADMode) != nil {
		logDedup("speech-vad", "voice detector unavailable, using loudness only: %v", err)
		return nil
	}
	return (&speechDetector{vad: vad}).isSpeech
}

func (d *speechDetector) isSpeech(frame []byte) bool {
	pieces, speech := 0, 0
	for start := 0; start+speechPieceSize <= len(frame); start += speechPieceSize {
		active, err := d.vad.Process(meetSampleRate, frame[start:start+speechPieceSize])
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

// isVoice combines both checks; a nil detector means loudness only.
func isVoice(detector func([]byte) bool, frame []byte, loud bool) bool {
	return loud && (detector == nil || detector(frame))
}
