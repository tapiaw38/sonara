package app

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"time"

	"sonara/internal/audio"
	"sonara/internal/meeting"
	"sonara/internal/pipewire"
	"sonara/internal/storage"
)

// The copilot also listens to the user's own microphone, only to remember
// what they actually said: it never triggers a reply. Those turns land in the
// conversation memory as "me", so suggestions build on, and do not
// contradict, what was already answered.
//
// Speakers make the microphone hear the other person too. The microphone goes
// through PipeWire's echo canceller (the one Practice uses), which removes
// what the speakers play, so the user's words are kept even while the other
// person is talking. Without it, a block during most of which the meeting
// audio also had voice is dropped as echo; overlap is counted frame by frame,
// so the other person answering right after does not erase what the user
// said. That fallback alone lost every word said over someone else.

// micEchoShare: above this share of the block with meeting voice, it is echo.
const micEchoShare = 0.3

// keepMicBlock decides whether a voiced microphone block is the user. With
// echo cancellation the speakers are already gone from it; without, a block
// mostly overlapping meeting voice is taken for echo.
func keepMicBlock(meetingVoiceShare float64, echoCancelled bool) bool {
	return echoCancelled || meetingVoiceShare <= micEchoShare
}

type micBlock struct {
	block   meeting.Block
	started time.Time // wall clock of the capture's first frame
}

func (a *App) listenMicForContext(ctx context.Context) {
	blocks := make(chan micBlock, 8)
	go a.rememberMicBlocks(ctx, blocks)
	defer close(blocks)
	defer a.echoCanceller.Stop()
	for ctx.Err() == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		target, err := a.practiceMicrophone(probeCtx)
		cancel()
		if err != nil {
			logDedup("copilot-mic", "copilot: no microphone for context: %v", err)
			if !waitForRetry(ctx) {
				return
			}
			continue
		}
		a.captureMicForContext(ctx, target, blocks)
		if !waitForRetry(ctx) {
			return
		}
	}
}

func (a *App) captureMicForContext(ctx context.Context, target string, blocks chan<- micBlock) {
	target = a.echoCanceller.Source(ctx, target)
	echoCancelled := target == pipewire.EchoCancelSource
	command := exec.CommandContext(ctx, "pw-record", "--target", target, "--latency", "40ms", "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	stdout, err := command.StdoutPipe()
	if err != nil || command.Start() != nil {
		logDedup("copilot-mic", "copilot: could not record the microphone: %v", err)
		return
	}
	defer command.Wait()
	logDedup("copilot-mic", "copilot: remembering what you say (%s)", target)
	started := time.Now()
	cutter := meeting.NewBlockCutter(storage.TrackMicrophone, audio.NewSpeechDetector())
	frame := make([]byte, audio.FrameBytes)
	var echoFrames int64
	for read := int64(0); ; read++ {
		if _, err := io.ReadFull(stdout, frame); err != nil {
			return
		}
		if cutter.Empty() {
			echoFrames = 0
		}
		// The meeting voice timestamp is fresh while the other person talks.
		if time.Now().UnixMilli()-a.lastOutputVoice.Load() <= 2*meeting.FrameMS {
			echoFrames++
		}
		block, ready := cutter.Add(frame, read*meeting.FrameMS)
		if !ready || !block.Voiced {
			continue
		}
		frames := (block.EndMS - block.StartMS) / meeting.FrameMS
		if share := float64(echoFrames) / float64(max(frames, 1)); !keepMicBlock(share, echoCancelled) {
			logf("copilot mic: dropped %.1fs block, the meeting had voice %.0f%% of it", float64(frames)/10, share*100)
			continue
		}
		select {
		case blocks <- micBlock{block: block, started: started}:
		default: // transcription is behind: losing a context line is fine
		}
	}
}

// rememberMicBlocks transcribes one block at a time, so turns stay in order.
func (a *App) rememberMicBlocks(ctx context.Context, blocks <-chan micBlock) {
	for item := range blocks {
		a.mu.RLock()
		backend, language, generation := a.backend, a.audioLanguage, a.sessionGeneration
		a.mu.RUnlock()
		if backend == nil || ctx.Err() != nil {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, err := backend.Transcribe(callCtx, audio.WAV(item.block.PCM), "me.wav", language)
		cancel()
		text := strings.TrimSpace(out.Text)
		if err != nil || text == "" {
			logf("copilot mic: nothing kept from a %.1fs block (err: %v)", float64(item.block.EndMS-item.block.StartMS)/1000, err)
			continue
		}
		logf("copilot mic: remembered %d chars of what you said", len(text))
		a.mu.Lock()
		if a.sessionGeneration == generation {
			a.rememberLocked("me", text)
		}
		a.mu.Unlock()
	}
}
