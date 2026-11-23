package app

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sonara/internal/audio"
	"sonara/internal/meeting"
	"sonara/internal/pipewire"
	"sonara/internal/storage"
)

func tone(amplitude int16) []byte {
	frame := make([]byte, audio.FrameBytes)
	for i := 0; i+1 < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], uint16(amplitude))
	}
	return frame
}

func testMeetingApp(t *testing.T) (*App, *storage.Store) {
	t.Helper()
	database, err := storage.OpenAt(filepath.Join(t.TempDir(), "sonara.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	a := NewApp()
	a.store = database
	return a, database
}

func testRun(t *testing.T, a *App, database *storage.Store) *meetingRun {
	t.Helper()
	if err := database.SaveSession("m1", storage.MeetingMode, "es"); err != nil {
		t.Fatal(err)
	}
	run := &meetingRun{
		sessionID: "m1", started: time.Now(), language: "es", state: meetingRecording,
		tracks: map[string]string{}, lastEnd: map[string]int64{}, failedBlock: map[int64]meeting.Block{},
	}
	a.meeting = run
	return run
}

func TestProcessBlockStoresTextAndEmitsEvent(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	var events []string
	var mu sync.Mutex
	a.emitHook = func(event string, _ any) { mu.Lock(); events = append(events, event); mu.Unlock() }
	a.meetingTranscribe = func(context.Context, []byte, string) (string, error) { return "  Buenos días  ", nil }
	run.pending = 1

	a.processBlock(run, meeting.Block{Track: storage.TrackMicrophone, StartMS: 12000, EndMS: 20000, PCM: tone(8000), Voiced: true})

	segments, _ := database.TranscriptSegments("m1")
	if len(segments) != 1 || segments[0].Text != "Buenos días" || segments[0].Status != storage.SegmentOK || segments[0].StartMS != 12000 {
		t.Fatalf("stored %+v", segments)
	}
	if run.pending != 0 {
		t.Errorf("pending = %d, want 0", run.pending)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(events, ",") != "meeting:segment,meeting:status" {
		t.Errorf("events = %v", events)
	}
}

func TestProcessBlockSkipsEmptyText(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	a.meetingTranscribe = func(context.Context, []byte, string) (string, error) { return "   ", nil }
	a.processBlock(run, meeting.Block{Track: storage.TrackOutput, PCM: tone(8000), Voiced: true})
	if segments, _ := database.TranscriptSegments("m1"); len(segments) != 0 {
		t.Fatalf("empty transcript stored as %+v", segments)
	}
}

func TestFailedBlockIsStoredAsGapAndRetried(t *testing.T) {
	retryDelay = func(int) time.Duration { return 0 }
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	calls := 0
	a.meetingTranscribe = func(context.Context, []byte, string) (string, error) {
		calls++
		return "", errors.New("503")
	}
	a.processBlock(run, meeting.Block{Track: storage.TrackOutput, StartMS: 5000, EndMS: 9000, PCM: tone(8000), Voiced: true})
	if calls != meetingRetries+1 {
		t.Errorf("tried %d times, want %d", calls, meetingRetries+1)
	}
	segments, _ := database.TranscriptSegments("m1")
	if len(segments) != 1 || segments[0].Status != storage.SegmentFailed || run.failed != 1 {
		t.Fatalf("a failed block must leave a visible failed row: %+v failed=%d", segments, run.failed)
	}

	a.meetingTranscribe = func(context.Context, []byte, string) (string, error) { return "recuperado", nil }
	n, err := a.RetryMeetingSegments("m1")
	if err != nil || n != 1 {
		t.Fatalf("retry = %d, %v", n, err)
	}
	segments, _ = database.TranscriptSegments("m1")
	if segments[0].Status != storage.SegmentOK || segments[0].Text != "recuperado" || run.failed != 0 {
		t.Errorf("after retry: %+v failed=%d", segments, run.failed)
	}
	if _, err := a.RetryMeetingSegments("other"); err == nil {
		t.Error("retry for a meeting whose audio is gone must say so")
	}
}

func TestEnqueueBlockOverflowRecordsGapAndSkipsSilence(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	run.jobs = make(chan meeting.Block, 1)

	a.enqueueBlock(run, meeting.Block{Track: storage.TrackMicrophone, StartMS: 0, EndMS: 1000, Voiced: false})
	if len(run.jobs) != 0 {
		t.Fatal("a silent block must not be queued")
	}
	a.enqueueBlock(run, meeting.Block{Track: storage.TrackMicrophone, StartMS: 0, EndMS: 1000, Voiced: true})
	a.enqueueBlock(run, meeting.Block{Track: storage.TrackMicrophone, StartMS: 1000, EndMS: 2000, Voiced: true})
	segments, _ := database.TranscriptSegments("m1")
	if len(segments) != 1 || segments[0].Status != storage.SegmentGap || segments[0].StartMS != 1000 {
		t.Fatalf("overflow must be a visible gap, got %+v", segments)
	}
	if run.pending != 1 {
		t.Errorf("pending = %d, want 1 (only the queued block)", run.pending)
	}
}

func TestStartMeetingRequiresMicrophoneAndStore(t *testing.T) {
	a := NewApp()
	if _, err := a.StartMeetingTranscript(); err == nil {
		t.Error("starting without a store must fail")
	}
	a, _ = testMeetingApp(t)
	if _, err := a.StartMeetingTranscript(); err == nil || !strings.Contains(err.Error(), "micrófono") {
		t.Errorf("starting without a microphone: %v", err)
	}
	if s := a.MeetingTranscriptStatus(); s.State != meetingIdle {
		t.Errorf("state = %q, want idle", s.State)
	}
}

func TestSegmentsListedAndSessionSummary(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 16000, EndMS: 20000, Text: "Sí, comparto pantalla.", Status: storage.SegmentOK})
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackMicrophone, StartMS: 12000, EndMS: 15000, Text: "Buenos días.", Status: storage.SegmentOK})
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 30000, EndMS: 40000, Status: storage.SegmentGap})

	view, err := a.MeetingTranscriptSegments("m1")
	if err != nil || len(view.Segments) != 3 || view.Segments[0].Text != "Buenos días." {
		t.Fatalf("segments must come back in time order: %+v, %v", view.Segments, err)
	}
	sessions, _ := database.RecentSessions(10)
	if len(sessions) != 1 || sessions[0].Mode != storage.MeetingMode || sessions[0].Messages != 2 || sessions[0].Preview != "Buenos días." {
		t.Errorf("summary = %+v, want 2 spoken segments and the first as preview", sessions)
	}
	if _, err := a.MeetingTranscriptSegments("missing"); err == nil {
		t.Error("unknown session must error")
	}
	database.SaveSession("copilot", "interview", "es")
	if _, err := a.MeetingTranscriptSegments("copilot"); err == nil {
		t.Error("a copilot session is not a meeting")
	}
}

func TestDeletingMeetingCascadesAndBlocksWhileRecording(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackMicrophone, Text: "x", Status: storage.SegmentOK})
	database.SaveSpeakerName("m1", storage.TrackMicrophone, "", "Walter")
	if _, err := a.DeleteSession("m1"); err == nil {
		t.Fatal("a meeting being recorded must not be deletable")
	}
	run.state = meetingIdle
	if _, err := a.DeleteSession("m1"); err != nil {
		t.Fatal(err)
	}
	segments, _ := database.TranscriptSegments("m1")
	speakers, _ := database.SpeakerNames("m1")
	if len(segments) != 0 || speakers[storage.TrackMicrophone] != "Yo" {
		t.Errorf("rows left behind: %d segments, microphone named %q", len(segments), speakers[storage.TrackMicrophone])
	}
}

func TestRenameSpeaker(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	names, err := a.RenameMeetingSpeaker("m1", storage.TrackOutput, "", "  Ana ")
	if err != nil || names[storage.TrackOutput] != "Ana" || names[storage.TrackMicrophone] != "Yo" {
		t.Fatalf("names = %v, %v", names, err)
	}
	names, _ = a.RenameMeetingSpeaker("m1", storage.TrackOutput, "", "")
	if names[storage.TrackOutput] != "Participante" {
		t.Errorf("empty name must restore the default, got %q", names[storage.TrackOutput])
	}
	if _, err := a.RenameMeetingSpeaker("m1", "bogus", "", "x"); err == nil {
		t.Error("unknown track accepted")
	}
}

func TestExportMeetingWritesAtomicallyAndAppendsExtension(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackMicrophone, StartMS: 1000, EndMS: 2000, Text: "Hola", Status: storage.SegmentOK})
	dir := t.TempDir()
	a.ctx = context.Background()
	a.saveDialog = func(_ context.Context, filename string) (string, error) {
		if !strings.HasPrefix(filename, "reunion-") || !strings.HasSuffix(filename, ".txt") {
			t.Errorf("default filename %q", filename)
		}
		return filepath.Join(dir, "salida"), nil
	}
	path, err := a.ExportMeetingTranscript("m1")
	if err != nil || filepath.Base(path) != "salida.txt" {
		t.Fatalf("path %q, err %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "[00:00:01] Yo: Hola") {
		t.Fatalf("file = %q, %v", data, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}

	a.saveDialog = func(context.Context, string) (string, error) { return "", nil }
	if path, err := a.ExportMeetingTranscript("m1"); path != "" || err != nil {
		t.Errorf("cancelled dialog = %q, %v; want silent no-op", path, err)
	}
}

func TestTrailingGapsCoverTracksThatNeverCaptured(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	run.started = time.Now().Add(-10 * time.Second)
	run.lastEnd[storage.TrackMicrophone] = 9900 // captured until just before the stop
	a.recordTrailingGaps(run)
	segments, _ := database.TranscriptSegments("m1")
	if len(segments) != 1 || segments[0].Track != storage.TrackOutput || segments[0].Status != storage.SegmentGap || segments[0].StartMS != 0 {
		t.Fatalf("only the track that never captured needs a gap: %+v", segments)
	}
}

func TestSetMicrophoneEndToEnd(t *testing.T) {
	a, database := testMeetingApp(t)
	a.microphonesHook = func(context.Context) ([]pipewire.Source, error) {
		return []pipewire.Source{{Target: "2152", App: "alsa_input.mic", Label: "Mic"}}, nil
	}
	got, err := a.SetMicrophone("alsa_input.mic")
	if err != nil || got != "alsa_input.mic" {
		t.Fatalf("got %q, %v", got, err)
	}
	if a.SelectedMicrophone() != "alsa_input.mic" {
		t.Errorf("SelectedMicrophone() = %q", a.SelectedMicrophone())
	}
	stored := database.Setting(storage.SettingMicrophone)
	if stored != "alsa_input.mic" {
		t.Errorf("stored microphone = %q, want it persisted", stored)
	}
	if _, err := a.SetMicrophone("gone"); err == nil {
		t.Fatal("selecting an unavailable microphone must error")
	}
	if _, err := a.SetMicrophone(""); err != nil || a.SelectedMicrophone() != "" {
		t.Errorf("an empty target must clear the selection, got %q, %v", a.SelectedMicrophone(), err)
	}
}
