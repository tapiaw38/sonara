package practice

import (
	"strings"
	"sync"
	"time"

	"sonara/backend/client"
	"sonara/internal/logx"
)

// Stream transcribes a spoken answer while it is being given, so the
// text is ready (almost) the moment the candidate stops talking and shows up
// word by word ("practice:partial"). Until voice is detected the buffer may
// hold the interviewer's echo or a cough: VoiceStarted clears it and resends
// exactly the segment the recorder kept. Any failure returns ok=false and the
// caller uploads the recording as before.
type Stream struct {
	session   *client.LiveSession
	onPartial func(string)

	prefix string // what was already said this turn, shown before the new words

	mu     sync.Mutex
	failed bool
	voiced bool
	text   string
	final  chan string
}

// Write feeds the stream, so io.TeeReader can copy the recorder's audio to it.
// It never fails: a broken stream falls back to uploading the answer.
// NewStream starts consuming session. prefix is what was already said this
// turn; onPartial receives the running text, prefix included.
func NewStream(session *client.LiveSession, prefix string, onPartial func(string)) *Stream {
	s := &Stream{session: session, onPartial: onPartial, prefix: prefix, final: make(chan string, 1)}
	go s.consume()
	return s
}

// Close abandons the stream without waiting for a transcript.
func (s *Stream) Close() { s.session.Close() }

func (s *Stream) Write(p []byte) (int, error) {
	s.append(p)
	return len(p), nil
}

func (s *Stream) append(pcm []byte) {
	s.mu.Lock()
	failed := s.failed
	s.mu.Unlock()
	if !failed && s.session.Append(pcm) != nil {
		s.fail()
	}
}

// VoiceStarted restarts the buffer from exactly what the recorder kept.
func (s *Stream) VoiceStarted(soFar []byte) {
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

func (s *Stream) consume() {
	for event := range s.session.Events() {
		switch event.Kind {
		case client.LiveDelta:
			s.mu.Lock()
			if !s.voiced {
				s.mu.Unlock()
				continue
			}
			s.text += event.Text
			text := strings.TrimSpace(s.prefix + " " + s.text)
			s.mu.Unlock()
			s.onPartial(text)
		case client.LiveCompleted:
			select {
			case s.final <- event.Text:
			default:
			}
		case client.LiveFailed, client.LiveClosed:
			if event.Err != nil {
				logx.Logf("practice streaming: %v", event.Err)
			}
			s.fail()
		}
	}
}

func (s *Stream) fail() {
	s.mu.Lock()
	s.failed = true
	s.mu.Unlock()
}

// Finish commits the answer and waits briefly for its transcript.
func (s *Stream) Finish() (string, bool) {
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
		logx.Logf("practice streaming: no transcript in time, uploading the answer")
		return "", false
	}
}
