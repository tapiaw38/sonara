package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// practiceAECSource is the echo-cancelled microphone PipeWire exposes while a
// practice runs. Measured on laptop speakers: the interviewer's echo drops by
// about 21 dB, which is what lets you talk over it without it hearing itself.
const practiceAECSource = "sonara-aec-source"

// practiceAECConfig runs PipeWire's WebRTC echo canceller as a separate client
// process. monitor.mode takes the reference from whatever the speakers play,
// so the interviewer's audio needs no rerouting.
const practiceAECConfig = `context.properties = { log.level = 2 }
context.spa-libs = { audio.convert.* = audioconvert/libspa-audioconvert support.* = support/libspa-support }
context.modules = [
  { name = libpipewire-module-rt flags = [ ifexists nofail ] }
  { name = libpipewire-module-protocol-native }
  { name = libpipewire-module-client-node }
  { name = libpipewire-module-adapter }
  { name = libpipewire-module-echo-cancel
    args = {
      library.name = aec/libspa-aec-webrtc
      monitor.mode = true
      audio.channels = 1
      # ffmpeg already denoises after this; a second suppressor (and the
      # transient one) ate word onsets, worst while talking over the interviewer.
      aec.args = { webrtc.noise_suppression = false webrtc.transient_suppression = false webrtc.gain_control = false }
      capture.props = { node.name = "sonara-aec-capture" node.passive = true target.object = %q }
      source.props = { node.name = "` + practiceAECSource + `" node.description = "Sonara (sin eco)" }
    }
  }
]
`

type practiceAEC struct {
	mic  string
	cmd  *exec.Cmd
	done chan struct{} // closed when the process exits
}

func (p *practiceAEC) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// practiceEchoCancelled returns the echo-cancelled source for mic, starting
// the canceller on first use. Any failure falls back to the raw mic: the
// practice still works, only talking over the interviewer gets harder.
func (a *App) practiceEchoCancelled(ctx context.Context, mic string) string {
	a.mu.Lock()
	running := a.practiceAEC
	a.mu.Unlock()
	if running != nil && running.mic == mic && running.alive() {
		return practiceAECSource
	}
	a.stopPracticeAEC()
	dir, err := os.UserCacheDir()
	if err != nil {
		return mic
	}
	path := filepath.Join(dir, "sonara", "practice-aec.conf")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(practiceAECConfig, mic)), 0o600); err != nil {
		logf("practice: echo cancel disabled: %v", err)
		return mic
	}
	cmd := exec.Command("pipewire", "-c", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM} // never outlives Sonara
	if err := cmd.Start(); err != nil {
		logf("practice: echo cancel disabled: %v", err)
		return mic
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	a.mu.Lock()
	a.practiceAEC = &practiceAEC{mic: mic, cmd: cmd, done: done}
	a.mu.Unlock()
	// pw-record silently falls back to the default mic on an unknown target,
	// so wait for the node to exist before pointing it there.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		out, _ := exec.CommandContext(ctx, "pw-cli", "ls", "Node").Output()
		if strings.Contains(string(out), `"`+practiceAECSource+`"`) {
			logf("practice: echo cancel on for %s", mic)
			return practiceAECSource
		}
		time.Sleep(150 * time.Millisecond)
	}
	logf("practice: echo cancel did not start, using the raw microphone")
	a.stopPracticeAEC()
	return mic
}

func (a *App) stopPracticeAEC() {
	a.mu.Lock()
	running := a.practiceAEC
	a.practiceAEC = nil
	a.mu.Unlock()
	if running != nil && running.cmd.Process != nil {
		running.cmd.Process.Signal(syscall.SIGTERM)
	}
}
