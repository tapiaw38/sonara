// Package diarize separates a meeting's participants by voice: who spoke when.
//
// Phase 2: speaker diarization of the meeting_output track, via the Gemini
// API (cloud) rather than a local model — no PyTorch, no local service, no
// Hugging Face token to install on the user's machine. It is optional,
// additive, and off unless a Gemini key is configured (Settings): a meeting
// transcribes exactly as in phase 1 whether or not this is set up, and
// nothing here ever blocks recording or transcription. If the call fails,
// participants simply stay "Participante" instead of "Participante 1/2/…".
//
// Unlike transcription, diarization runs once, after capture stops, on the
// whole accumulated meeting_output track rather than per block: a fresh
// Gemini call has no memory of a previous one, so diarizing short blocks
// independently would give each one its own, mutually incompatible "Speaker
// 1" — stable identity across the meeting needs the model to see it all at
// once. That means this track's audio — never the microphone's — is held in
// memory for the length of the meeting instead of being discarded per block,
// and only when a Gemini key is configured; the caller caps how much
// (diarizeMaxDuration in the app).
//
// This also means the microphone is never diarized or voice-matched: "Yo" is
// assigned by which device captured it, not by analyzing the audio. Matching a
// user's own voice inside the meeting_output mix, to catch an echo of
// themselves through someone else's speakers, is phase 3 — see voicematch.go.
//
// *** UNVERIFIED AGAINST A REAL KEY ***: this talks to
// generativelanguage.googleapis.com's Files and Interactions APIs per their
// public docs (ai.google.dev, fetched 2026-11-16), but has never been run
// against a real Gemini account — see PLAN_TRANSCRIPCION_REUNIONES.md.
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
