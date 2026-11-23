package practice

import (
	"context"
	"io"
	"os/exec"
	"time"

	"sonara/internal/audio"
)

// Filter before VAD so steady fan or room noise is less likely to start a turn.
// Keep raw capture available on installations without FFmpeg.
func NoiseFilter(ctx context.Context, input io.Reader) (*exec.Cmd, io.Reader) {
	filter := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-probesize", "32", "-analyzeduration", "0",
		"-f", "s16le", "-ar", "16000", "-ac", "1", "-blocksize", "3200", "-i", "pipe:0",
		"-af", "afftdn=nr=12:nf=-45:tn=1:ad=0.7",
		"-flush_packets", "1", "-f", "s16le", "pipe:1")
	filter.Stdin = input
	output, err := filter.StdoutPipe()
	if err != nil {
		return nil, nil
	}
	if err := filter.Start(); err != nil {
		output.Close()
		return nil, nil
	}
	return filter, output
}

// LevelReader reports the microphone level ~10 times a second so the
// voice circle can ripple with how loud the candidate speaks.
// NewLevelReader wraps r, calling emit with the level of what it reads.
func NewLevelReader(r io.Reader, emit func(float64)) *LevelReader {
	return &LevelReader{reader: r, emit: emit}
}

type LevelReader struct {
	reader io.Reader
	emit   func(float64)
	last   time.Time
}

func (r *LevelReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n >= 2 && time.Since(r.last) >= 90*time.Millisecond {
		r.last = time.Now()
		r.emit(audio.RMS(p[:n-n%2]))
	}
	return n, err
}
