package main

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	sonaraBackend "sonara/backend/client"
)

// Runs the real recorder against this machine's PipeWire microphone. Opt in:
//
//	SONARA_REAL_AUDIO=1 go test -run TestMeetingRealCapture -v .
func TestMeetingRealCapture(t *testing.T) {
	if os.Getenv("SONARA_REAL_AUDIO") == "" {
		t.Skip("set SONARA_REAL_AUDIO=1 to record from the real microphone")
	}
	a, _ := testMeetingApp(t)
	a.backend = sonaraBackend.New()
	var mu sync.Mutex
	var events []string
	a.emitHook = func(event string, _ any) { mu.Lock(); events = append(events, event); mu.Unlock() }
	var sent int
	a.meetingTranscribe = func(_ context.Context, wav []byte, _ string) (string, error) {
		mu.Lock()
		sent++
		mu.Unlock()
		return "bloque", nil
	}
	mics, err := a.Microphones()
	if err != nil || len(mics) == 0 {
		t.Skipf("no microphone: %v", err)
	}
	if _, err := a.SetMicrophone(mics[0].Target); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartMeetingTranscript(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	status := a.MeetingTranscriptStatus()
	t.Logf("while recording: %+v", status)
	if status.State != meetingRecording || status.MicrophoneState != "ok" {
		t.Errorf("microphone track not capturing: %+v", status)
	}
	start := time.Now()
	a.StopMeetingTranscript()
	for a.MeetingTranscriptStatus().State != meetingIdle {
		if time.Since(start) > 10*time.Second {
			t.Fatal("stop did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("stopped and drained in %v", time.Since(start))
	view, err := a.MeetingTranscriptSegments(status.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("segments=%+v blocksSent=%d events=%d", view.Segments, sent, len(events))
}
