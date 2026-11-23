// Package practice holds the voice-practice logic that needs no app state:
// when the candidate's answer starts and ends (and when they talk over the
// interviewer), the audio filters around the microphone, and the prompts.
package practice

import (
	"context"
	"fmt"
	"io"
	"math"
	"slices"

	"sonara/internal/audio"
	"sonara/internal/logx"
)

const (
	minStartFrames = 2
	leadFrames     = 2
	minVoiceFrames = 5
	// 1s cut answers mid-sentence: pauses to think (more so in a second
	// language) run longer than that.
	endSilenceFrames = 15
	InterruptFrames  = 6 // frames over the echo within the window, raw mic
	// With echo cancellation the interviewer leaves 0-1/8 frames, and talking
	// over it reached 4-5/8 in the logs: 4 cuts in without cutting itself off.
	InterruptFramesAEC = 4
	interruptWindow    = 8
	interruptFloor     = 0.012
	// The first second of playback only learns the echo: before that the
	// estimate is empty and the interviewer's first syllables would cut it off.
	echoWarmupFrames = 10
	echoTailFrames   = 8 // 800ms ignored right after the interviewer stops
	// An answer is handed over after 15s of continuous speech; the trailing
	// pause is trimmed to keepSilenceFrames, never below minAnswerFrames.
	maxAnswerFrames   = 150
	keepSilenceFrames = 3
	minAnswerFrames   = 10
)

// ReadAnswer reads 100ms frames from input until the candidate has spoken and
// paused, and returns that answer. While playing() the interviewer is talking:
// frames then only count if they rise over its echo, and enough of them cut it
// off (onVoice fires as soon as an answer is under way).
// speech is the voice detector; nil counts every loud frame as speech.
// interruptFrames is how many frames of the window must be voice to cut the
// interviewer off.
func ReadAnswer(ctx context.Context, interruptFrames int, input io.Reader, playing func() bool, speech func([]byte) bool, onVoice func(soFar []byte)) ([]byte, error) {
	frame := make([]byte, audio.FrameBytes)
	segment := make([]byte, 0, audio.SampleRate*2*12)
	ambient, echo := &audio.NoiseFloor{}, &audio.NoiseFloor{}
	// window holds the last frames heard while the interviewer speaks. Speech
	// dips between syllables, so an interruption needs most of the window over
	// the echo, not an unbroken run (that almost never happened on speakers).
	type bargeFrame struct {
		pcm  []byte
		over bool
	}
	var window []bargeFrame
	var bestRun int
	var peak float64
	var wasPlaying bool
	var playedFrames int
	var echoTail int
	var recording bool
	var startFrames, voicedFrames, silenceFrames int
	for {
		if _, err := io.ReadFull(input, frame); err != nil {
			switch ctx.Err() {
			case context.DeadlineExceeded:
				return nil, fmt.Errorf("se agotó el tiempo de escucha; revisá el micrófono y probá de nuevo")
			case context.Canceled:
				return nil, ctx.Err()
			}
			if recording && voicedFrames >= minVoiceFrames {
				return segment, nil
			}
			return nil, fmt.Errorf("se cortó la escucha antes de captar una respuesta")
		}
		level := audio.RMS(frame)
		if !recording && playing() {
			wasPlaying = true
			playedFrames++
			if playedFrames <= echoWarmupFrames {
				echo.Observe(level)
				continue
			}
			bar := math.Max(interruptFloor, echo.Threshold())
			peak = math.Max(peak, level/bar)
			loudVoice := audio.IsVoice(speech, frame, level > bar)
			// While an interruption is building, your voice must not count as
			// echo: it would raise the bar over itself and never cut in.
			building := slices.ContainsFunc(window, func(f bargeFrame) bool { return f.over })
			if !loudVoice || !building {
				echo.Observe(level)
			}
			window = append(window, bargeFrame{pcm: append([]byte(nil), frame...), over: loudVoice})
			if len(window) > interruptWindow {
				window = window[1:]
			}
			over, first := 0, -1
			for i, f := range window {
				if f.over {
					over++
					if first < 0 {
						first = i
					}
				}
			}
			bestRun = max(bestRun, over)
			if over >= interruptFrames {
				segment = segment[:0]
				for _, f := range window[first:] {
					segment = append(segment, f.pcm...)
				}
				recording, voicedFrames = true, over
				window = nil
				logx.Logf("practice: interrupted by voice (%.1f× the echo)", peak)
				onVoice(segment)
			}
			continue
		}
		if wasPlaying {
			wasPlaying, playedFrames = false, 0
			echoTail = echoTailFrames
			// Voice still going when playback ended: the answer started there.
			tail := len(window)
			for tail > 0 && window[tail-1].over {
				tail--
			}
			if !recording {
				logx.Dedup("practice-barge", "practice: no interruption during playback (best %d/%d frames over the echo, peak %.1f×)", bestRun, interruptWindow, peak)
			}
			trailing := window[tail:]
			window, bestRun, peak = nil, 0, 0
			if !recording && len(trailing) > 0 {
				segment = segment[:0]
				for _, f := range trailing {
					segment = append(segment, f.pcm...)
				}
				startFrames = len(trailing)
				if startFrames >= minStartFrames {
					recording, voicedFrames = true, startFrames
					if voicedFrames >= minVoiceFrames {
						onVoice(segment)
					}
				}
			}
		}
		// The interviewer's voice keeps ringing in the room (and in the output
		// buffer) after playback "ended": that tail is not the candidate.
		if echoTail > 0 && !recording {
			echoTail--
			continue
		}
		loud := level >= ambient.Threshold()
		speechLike := speech == nil || speech(frame)
		// Learn the room from anything that is not voice, not only from what
		// is already below the bar: room noise above the initial floor would
		// otherwise never be learned, every frame stays "loud" and a pause is
		// only noticed when the VAD happens to call noise silence (rarely: the
		// answer then ran to the 15s limit).
		if !loud || !speechLike {
			ambient.Observe(level)
		}
		voice := loud && speechLike
		if !recording {
			if voice {
				startFrames++
				segment = append(segment, frame...)
				if startFrames >= minStartFrames {
					recording, voicedFrames, silenceFrames = true, startFrames, 0
				}
			} else {
				startFrames = 0
				segment = append(segment, frame...)
				if keep := leadFrames * audio.FrameBytes; len(segment) > keep {
					segment = append(segment[:0], segment[len(segment)-keep:]...)
				}
			}
			continue
		}
		segment = append(segment, frame...)
		if voice {
			voicedFrames++
			if voicedFrames == minVoiceFrames {
				onVoice(segment)
			}
			silenceFrames = 0
		} else {
			silenceFrames++
		}
		ended := silenceFrames >= endSilenceFrames
		if !ended && len(segment)/audio.FrameBytes < maxAnswerFrames {
			continue
		}
		if voicedFrames >= minVoiceFrames {
			return audio.TrimTrailingSilence(segment, silenceFrames, keepSilenceFrames, minAnswerFrames), nil
		}
		// Discard short bursts so the next answer starts a fresh segment.
		recording = false
		startFrames, voicedFrames, silenceFrames = 0, 0, 0
		keep := leadFrames * audio.FrameBytes
		segment = append(segment[:0], segment[len(segment)-keep:]...)
	}
}
