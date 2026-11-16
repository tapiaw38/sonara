package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// voiceSampleSeconds is how long RecordVoiceSample captures. Long enough for
// a few natural sentences (the plan called for 5–10s); short enough to stay a
// quick, one-time action rather than something that feels like a chore.
const voiceSampleSeconds = 8

// voiceSamplePath lives beside the database, not inside it: it is the one
// piece of audio this app keeps on purpose (everything meeting-related is
// discarded after use), so it gets its own file a user can find and delete.
func voiceSamplePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "sonara", "voice-sample.wav"), nil
}

// HasVoiceSample reports whether a reference recording exists, for the UI to
// show "grabada" vs "sin grabar" without reading the file itself.
func (a *App) HasVoiceSample() bool {
	path, err := voiceSamplePath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// RecordVoiceSample captures a short, voluntary reference of the user's own
// voice from the selected microphone, for phase-3 echo detection. It blocks
// for voiceSampleSeconds: a one-time, explicit action the user watches
// happen, not a background task.
func (a *App) RecordVoiceSample() error {
	a.mu.RLock()
	mic := a.preferredMic
	a.mu.RUnlock()
	if mic == "" {
		return fmt.Errorf("elegí un micrófono antes de grabar la muestra")
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 5*time.Second)
	target, err := a.meetingTarget(probeCtx, trackMicrophone)
	cancelProbe()
	if err != nil {
		return fmt.Errorf("el micrófono no está disponible: %w", err)
	}

	recordCtx, stop := context.WithTimeout(context.Background(), voiceSampleSeconds*time.Second)
	defer stop()
	command := exec.CommandContext(recordCtx, "pw-record", "--target", target, "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	stdout, err := command.StdoutPipe()
	if err != nil || command.Start() != nil {
		return fmt.Errorf("no se pudo grabar: %w", err)
	}
	// Reads until pw-record's pipe closes, which the context deadline forces
	// by killing the process — the same shutdown mechanism captureTrack relies on.
	pcm, readErr := io.ReadAll(stdout)
	command.Wait()
	if readErr != nil && len(pcm) == 0 {
		return fmt.Errorf("no se pudo leer el audio: %w", readErr)
	}
	if len(pcm) < meetSampleRate*2 { // under 1s captured: something went wrong
		return fmt.Errorf("se grabó muy poco audio; probá de nuevo")
	}
	path, err := voiceSamplePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, wavFromPCM(pcm))
}

// DeleteVoiceSample turns phase-3 echo detection back off: with no sample,
// runVoiceMatch has nothing to compare against and becomes a no-op again.
func (a *App) DeleteVoiceSample() error {
	path, err := voiceSamplePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// readVoiceSample is nil, nil when nothing was ever recorded — not an error:
// callers treat "no sample" as "feature off", not as a failure to report.
func readVoiceSample() ([]byte, error) {
	path, err := voiceSamplePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no hay muestra de voz grabada")
	}
	return data, err
}
