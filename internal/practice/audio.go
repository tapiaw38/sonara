package practice

import (
	"io"
	"time"

	"sonara/internal/audio"
)

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
