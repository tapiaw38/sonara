package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	sonaraBackend "sonara/backend/client"
)

// practiceStream transcribes a spoken answer while it is being given, so the
// text is ready (almost) the moment the candidate stops talking and shows up
// word by word ("practice:partial"). Until voice is detected the buffer may
// hold the interviewer's echo or a cough: voiceStarted clears it and resends
// exactly the segment the recorder kept. Any failure returns ok=false and the
// caller uploads the recording as before.
type practiceStream struct {
	a       *App
	session *sonaraBackend.LiveSession

	prefix string // what was already said this turn, shown before the new words

	mu     sync.Mutex
	failed bool
	voiced bool
	text   string
	final  chan string
}

func (a *App) openPracticeStream(language string) *practiceStream {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	if backend == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	session, err := backend.OpenLive(ctx, language)
	if err != nil {
		logDedup("practice-stream", "practice streaming off, uploading answers: %v", err)
		return nil
	}
	s := &practiceStream{a: a, session: session, final: make(chan string, 1)}
	go s.consume()
	return s
}

func (s *practiceStream) append(pcm []byte) {
	s.mu.Lock()
	failed := s.failed
	s.mu.Unlock()
	if !failed && s.session.Append(pcm) != nil {
		s.fail()
	}
}

// voiceStarted restarts the buffer from exactly what the recorder kept.
func (s *practiceStream) voiceStarted(soFar []byte) {
	s.mu.Lock()
	if s.failed {
		s.mu.Unlock()
		return
	}
	s.voiced, s.text = true, ""
	s.mu.Unlock()
	if s.session.Clear() != nil || s.session.Append(soFar) != nil {
		s.fail()
	}
}

func (s *practiceStream) consume() {
	for event := range s.session.Events() {
		switch event.Kind {
		case sonaraBackend.LiveDelta:
			s.mu.Lock()
			if !s.voiced {
				s.mu.Unlock()
				continue
			}
			s.text += event.Text
			text := strings.TrimSpace(s.prefix + " " + s.text)
			s.mu.Unlock()
			s.a.emit("practice:partial", text)
		case sonaraBackend.LiveCompleted:
			select {
			case s.final <- event.Text:
			default:
			}
		case sonaraBackend.LiveFailed, sonaraBackend.LiveClosed:
			if event.Err != nil {
				logf("practice streaming: %v", event.Err)
			}
			s.fail()
		}
	}
}

func (s *practiceStream) fail() {
	s.mu.Lock()
	s.failed = true
	s.mu.Unlock()
}

// finish commits the answer and waits briefly for its transcript.
func (s *practiceStream) finish() (string, bool) {
	defer s.session.Close()
	s.mu.Lock()
	failed := s.failed
	s.mu.Unlock()
	if failed || s.session.Commit() != nil {
		return "", false
	}
	select {
	case text := <-s.final:
		return strings.TrimSpace(text), true
	case <-time.After(6 * time.Second):
		logf("practice streaming: no transcript in time, uploading the answer")
		return "", false
	}
}

// practiceTee feeds every byte the recorder reads to the stream as well.
type practiceTee struct {
	reader io.Reader
	stream *practiceStream
}

func (t practiceTee) Read(p []byte) (int, error) {
	n, err := t.reader.Read(p)
	if n > 0 {
		t.stream.append(p[:n])
	}
	return n, err
}

// practiceLevelReader reports the microphone level ~10 times a second so the
// voice circle can ripple with how loud the candidate speaks.
type practiceLevelReader struct {
	reader io.Reader
	emit   func(float64)
	last   time.Time
}

func (r *practiceLevelReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n >= 2 && time.Since(r.last) >= 90*time.Millisecond {
		r.last = time.Now()
		r.emit(pcmRMS(p[:n-n%2]))
	}
	return n, err
}
