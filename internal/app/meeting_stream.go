package app

import (
	"context"
	"strings"
	"sync"
	"time"

	sonaraBackend "sonara/backend/client"
	"sonara/internal/meeting"
	"sonara/internal/storage"
)

// meetingStream sends one track's audio to OpenAI's streaming transcription
// while it is recorded, so words show up as they are spoken ("meeting:partial")
// instead of a block at a time. The block cutter still decides where a block
// ends: that is the commit, and the committed text is stored exactly like an
// uploaded block's. Any streaming failure hands its blocks back to the upload
// path (enqueueBlock), so a broken connection costs latency, never text.
type meetingStream struct {
	a       *App
	run     *meetingRun
	track   string
	session *sonaraBackend.LiveSession

	mu       sync.Mutex
	failed   bool
	queue    []meeting.Block          // committed, waiting for the server's item id
	byItem   map[string]meeting.Block // committed, waiting for the transcript
	partials map[string]string        // item id -> text so far
	done     chan struct{}
}

type MeetingPartialEvent struct {
	SessionID string `json:"sessionId"`
	Track     string `json:"track"`
	Text      string `json:"text"`
}

// openMeetingStream returns nil when streaming is not available (another
// provider, no key, no network): the caller then uploads blocks as before.
func (a *App) openMeetingStream(run *meetingRun, track string) *meetingStream {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	if backend == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := backend.OpenMeetingLive(ctx, run.language)
	if err != nil {
		logDedup("meeting-stream-"+track, "meeting %s: %s streaming off, uploading blocks: %v", run.sessionID, track, err)
		return nil
	}
	logf("meeting %s: %s streaming on", run.sessionID, track)
	s := &meetingStream{a: a, run: run, track: track, session: session,
		byItem: map[string]meeting.Block{}, partials: map[string]string{}, done: make(chan struct{})}
	go s.consume()
	return s
}

func (s *meetingStream) append(frame []byte) {
	s.mu.Lock()
	failed := s.failed
	s.mu.Unlock()
	if !failed && s.session.Append(frame) != nil {
		s.fail()
	}
}

// take ends a block on the stream. False means the caller must upload it.
func (s *meetingStream) take(block meeting.Block) bool {
	s.mu.Lock()
	if s.failed {
		s.mu.Unlock()
		return false
	}
	if !block.Voiced {
		s.mu.Unlock()
		// Silence is never billed for transcription: drop it from the buffer.
		if s.session.Clear() != nil {
			s.fail()
		}
		s.clearPartial()
		return true
	}
	s.queue = append(s.queue, block)
	s.mu.Unlock()
	s.run.mu.Lock()
	s.run.pending++
	s.run.mu.Unlock()
	if s.session.Commit() != nil {
		s.fail()
	}
	return true
}

func (s *meetingStream) consume() {
	defer close(s.done)
	for event := range s.session.Events() {
		switch event.Kind {
		case sonaraBackend.LiveCommitted:
			s.mu.Lock()
			if len(s.queue) > 0 {
				s.byItem[event.ItemID] = s.queue[0]
				s.queue = s.queue[1:]
			}
			s.mu.Unlock()
		case sonaraBackend.LiveDelta:
			s.mu.Lock()
			s.partials[event.ItemID] += event.Text
			text := s.partials[event.ItemID]
			s.mu.Unlock()
			s.a.emit("meeting:partial", MeetingPartialEvent{SessionID: s.run.sessionID, Track: s.track, Text: text})
		case sonaraBackend.LiveCompleted:
			s.mu.Lock()
			block, ok := s.byItem[event.ItemID]
			delete(s.byItem, event.ItemID)
			delete(s.partials, event.ItemID)
			s.mu.Unlock()
			if ok {
				s.finishBlock()
				logf("meeting %s: %s streamed block %d-%dms (%d chars)", s.run.sessionID, s.track, block.StartMS, block.EndMS, len(event.Text))
				s.a.storeBlockText(s.run, block, event.Text)
			}
			s.clearPartial()
		case sonaraBackend.LiveFailed:
			s.mu.Lock()
			block, ok := s.byItem[event.ItemID]
			delete(s.byItem, event.ItemID)
			s.mu.Unlock()
			if !ok {
				logf("meeting %s: %s streaming failed: %v", s.run.sessionID, s.track, event.Err)
				s.fail()
				continue
			}
			s.finishBlock()
			s.a.enqueueBlock(s.run, block)
		case sonaraBackend.LiveClosed:
			if event.Err != nil {
				logf("meeting %s: %s streaming closed: %v", s.run.sessionID, s.track, event.Err)
			}
			s.fail()
		}
	}
}

func (s *meetingStream) finishBlock() {
	s.run.mu.Lock()
	s.run.pending--
	s.run.mu.Unlock()
}

func (s *meetingStream) clearPartial() {
	s.a.emit("meeting:partial", MeetingPartialEvent{SessionID: s.run.sessionID, Track: s.track})
}

// fail stops streaming and uploads every block still waiting on it.
func (s *meetingStream) fail() {
	s.mu.Lock()
	if s.failed {
		s.mu.Unlock()
		return
	}
	s.failed = true
	orphans := append([]meeting.Block(nil), s.queue...)
	for _, block := range s.byItem {
		orphans = append(orphans, block)
	}
	s.queue, s.byItem = nil, map[string]meeting.Block{}
	s.mu.Unlock()
	go s.session.Close()
	s.clearPartial()
	for _, block := range orphans {
		s.finishBlock()
		s.a.enqueueBlock(s.run, block)
	}
}

// finish waits for the last transcripts once capture ended, bounded, then
// uploads whatever never came back. Runs before the meeting closes its queue.
func (s *meetingStream) finish() {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		idle := s.failed || len(s.queue)+len(s.byItem) == 0
		s.mu.Unlock()
		if idle {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.fail()
	<-s.done
}

// storeBlockText stores a transcribed block, the same way for an uploaded or
// a streamed one. Empty text means nothing was said: nothing is stored.
func (a *App) storeBlockText(run *meetingRun, block meeting.Block, text string) {
	if text = strings.TrimSpace(text); text != "" {
		a.storeSegment(run, storage.TranscriptSegment{Track: block.Track, StartMS: block.StartMS, EndMS: block.EndMS, Text: text, Status: storage.SegmentOK})
	}
	a.emitMeetingStatus()
}
