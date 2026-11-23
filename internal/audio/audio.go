// Package audio holds the PCM format Sonara records in (16 kHz mono s16) and
// the signal checks built on it: levels, ambient noise, voice detection, WAV.
package audio

import (
	"encoding/binary"
	"math"
	"sort"
)

const (
	SampleRate    = 16000
	FrameBytes    = 3200 // 100ms mono s16 PCM
	NoiseWindow   = 30   // 3s of quiet frames back the ambient estimate
	NoiseMargin   = 1.8  // how far above the ambient hiss a voice must sit
	MinNoiseFloor = 0.004
)

// NoiseFloor tracks the ambient level of the call so the voice threshold sits
// above whatever hiss this room, headset and codec produce rather than above a
// constant. Only frames that were not treated as voice feed the estimate, so a
// long intervention cannot drag the floor up behind itself.
type NoiseFloor struct {
	quiet []float64
}

// Observe feeds one non-voice frame level into the ambient estimate.
func (n *NoiseFloor) Observe(level float64) {
	n.quiet = append(n.quiet, level)
	if len(n.quiet) > NoiseWindow {
		n.quiet = n.quiet[len(n.quiet)-NoiseWindow:]
	}
}

// Empty reports whether no ambient level has been observed yet.
func (n *NoiseFloor) Empty() bool { return len(n.quiet) == 0 }

// Threshold is the median ambient level times a margin, never below the
// absolute floor: a perfectly silent stream must not make every frame voice.
func (n *NoiseFloor) Threshold() float64 {
	if len(n.quiet) == 0 {
		return MinNoiseFloor
	}
	sorted := append([]float64(nil), n.quiet...)
	sort.Float64s(sorted)
	return math.Max(sorted[len(sorted)/2]*NoiseMargin, MinNoiseFloor)
}

// RMS is the root-mean-square level of a PCM frame, 0 (silence) to 1.
func RMS(frame []byte) float64 {
	var sum float64
	for i := 0; i+1 < len(frame); i += 2 {
		sample := float64(int16(binary.LittleEndian.Uint16(frame[i:i+2]))) / 32768
		sum += sample * sample
	}
	return math.Sqrt(sum / float64(len(frame)/2))
}

// WAV wraps raw PCM in a 44-byte WAV header for upload.
func WAV(pcm []byte) []byte {
	dataSize := uint32(len(pcm))
	wav := make([]byte, 44+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], 36+dataSize)
	copy(wav[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], SampleRate)
	binary.LittleEndian.PutUint32(wav[28:32], SampleRate*2)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], dataSize)
	copy(wav[44:], pcm)
	return wav
}

// TailFrames returns a copy of the last n frames of a segment.
func TailFrames(segment []byte, n int) []byte {
	if size := n * FrameBytes; size < len(segment) {
		return append([]byte(nil), segment[len(segment)-size:]...)
	}
	return append([]byte(nil), segment...)
}

// TrimTrailingSilence drops the silence that ended a segment, keeping
// keepFrames of it so the last word is not clipped, and never cutting the
// segment below minFrames. A shorter upload is a faster transcription.
func TrimTrailingSilence(segment []byte, silenceFrames, keepFrames, minFrames int) []byte {
	drop := silenceFrames - keepFrames
	if drop <= 0 {
		return segment
	}
	trimmed := len(segment) - drop*FrameBytes
	if trimmed < minFrames*FrameBytes {
		return segment
	}
	return segment[:trimmed]
}
