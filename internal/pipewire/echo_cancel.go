package pipewire

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"sonara/internal/logx"
)

// EchoCancelSource is the echo-cancelled microphone PipeWire exposes while an
// EchoCanceller runs. Measured on laptop speakers: the interviewer's echo drops
// by about 21 dB, which is what lets you talk over it without it hearing itself.
const EchoCancelSource = "sonara-aec-source"

// echoCancelConfig runs PipeWire's WebRTC echo canceller as a separate client
// process. monitor.mode takes the reference from whatever the speakers play,
// so the interviewer's audio needs no rerouting.
const echoCancelConfig = `context.properties = { log.level = 2 }
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
      source.props = { node.name = "` + EchoCancelSource + `" node.description = "Sonara (sin eco)" }
    }
  }
]
`

// EchoCanceller owns one echo-cancel process for one microphone. The zero
// value is ready to use and safe for concurrent callers.
type EchoCanceller struct {
	mu   sync.Mutex
	mic  string
	cmd  *exec.Cmd
	done chan struct{} // closed when the process exits
}

func (e *EchoCanceller) runningFor(mic string) bool {
	if e.cmd == nil || e.mic != mic {
		return false
	}
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}

// Source returns the echo-cancelled source for mic, starting the canceller on
// first use. Any failure falls back to the raw mic: recording still works,
// only talking over the speakers gets harder.
func (e *EchoCanceller) Source(ctx context.Context, mic string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runningFor(mic) {
		return EchoCancelSource
	}
	e.stopLocked()
	if err := e.startLocked(mic); err != nil {
		logx.Logf("practice: echo cancel disabled: %v", err)
		return mic
	}
	// pw-record silently falls back to the default mic on an unknown target,
	// so wait for the node to exist before pointing it there.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; {
		out, _ := exec.CommandContext(ctx, "pw-cli", "ls", "Node").Output()
		if strings.Contains(string(out), `"`+EchoCancelSource+`"`) {
			logx.Logf("practice: echo cancel on for %s", mic)
			return EchoCancelSource
		}
		time.Sleep(150 * time.Millisecond)
	}
	logx.Logf("practice: echo cancel did not start, using the raw microphone")
	e.stopLocked()
	return mic
}

func (e *EchoCanceller) startLocked(mic string) error {
	dir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	// Absolute path: pipewire -c resolves a relative one against its own
	// config directories and fails silently.
	path := filepath.Join(dir, "sonara", "practice-aec.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(echoCancelConfig, mic)), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("pipewire", "-c", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM} // never outlives Sonara
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	e.mic, e.cmd, e.done = mic, cmd, done
	return nil
}

// Stop ends the canceller if it runs. Safe to call at any time.
func (e *EchoCanceller) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
}

func (e *EchoCanceller) stopLocked() {
	if e.cmd != nil && e.cmd.Process != nil {
		e.cmd.Process.Signal(syscall.SIGTERM)
	}
	e.mic, e.cmd, e.done = "", nil, nil
}
