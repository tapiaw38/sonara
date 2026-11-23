// Package diarize separates a meeting's participants by voice: who spoke when.
//
// A configured Gemini key selects the cloud engine. Without one, the service
// selects local sherpa-onnx. Cloud failures are reported to the caller.
//
// Cloud passes send the accumulated meeting_output recording so the model can
// label speakers across the recording. Echo detection also sends an optional,
// separately recorded voice sample. The app caps the buffered meeting audio.
//
// The Files API v1beta and Interactions API v1 contract is covered by HTTP
// tests. The integration and the accuracy of its speaker and echo intervals
// still need validation against Gemini with a real key.
package diarize

import (
	"context"
	"time"

	"sonara/internal/audio"
)

// FinalTimeout bounds the whole final pass: one file upload plus one model
// call, for up to three hours of audio.
const FinalTimeout = 15 * time.Minute

const (
	// CloudMinGrowth is how much new audio (60 s) a pass needs to be worth
	// re-uploading the whole recording again.
	CloudMinGrowth = 60 * audio.SampleRate * 2
	// The local engine costs nothing per pass, so it re-separates far more
	// often: letters show up within seconds instead of minutes. Each pass still
	// reads the whole recording (about 0.1s of CPU per second of audio), so a
	// very long meeting makes each pass slower.
	localMinGrowth = 10 * audio.SampleRate * 2
)

// Span is one speaker turn, in milliseconds from the start of the audio.
type Span struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Speaker string `json:"speaker"`
}

// Engine is one way of separating speakers (Strategy): Gemini in the cloud or
// sherpa-onnx locally. speakers fixes how many people to find; 0 guesses.
type Engine interface {
	Diarize(ctx context.Context, wav []byte, speakers int) ([]Span, error)
}

// Service picks the engine for each pass: Gemini when its key is set,
// otherwise the local one. Setting a key in Settings is the opt-in, with no
// separate feature flag.
type Service struct {
	gemini *Gemini
	local  Sherpa
	// CloudPeriod and LocalPeriod space the passes while a meeting runs.
	// Fields so tests can shorten them.
	CloudPeriod, LocalPeriod time.Duration
}

func NewService(gemini *Gemini) *Service {
	return &Service{gemini: gemini, CloudPeriod: 3 * time.Minute, LocalPeriod: 20 * time.Second}
}

func (s *Service) SetAPIKey(key string) { s.gemini.SetAPIKey(key) }

// HasCloudKey reports whether Gemini is set up; echo detection needs it.
func (s *Service) HasCloudKey() bool { return s.gemini.key() != "" }

// Configured reports whether any engine can run.
func (s *Service) Configured() bool { return s.HasCloudKey() || s.local.Available() }

func (s *Service) engine() Engine {
	if s.HasCloudKey() {
		return s.gemini
	}
	return s.local
}

func (s *Service) Diarize(ctx context.Context, wav []byte, speakers int) ([]Span, error) {
	return s.engine().Diarize(ctx, wav, speakers)
}

// DetectEcho finds where the voice in sampleWav is heard inside meetingWav.
func (s *Service) DetectEcho(ctx context.Context, sampleWav, meetingWav []byte) ([]EchoSpan, error) {
	return s.gemini.DetectEcho(ctx, sampleWav, meetingWav)
}

// Period is the wait between passes during a meeting.
func (s *Service) Period() time.Duration {
	if s.HasCloudKey() {
		return s.CloudPeriod
	}
	return s.LocalPeriod
}

// MinGrowth is how much new audio, in bytes, a pass needs.
func (s *Service) MinGrowth() int {
	if s.HasCloudKey() {
		return CloudMinGrowth
	}
	return localMinGrowth
}
