package main

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
)

func tone(amplitude int16) []byte {
	frame := make([]byte, meetFrameBytes)
	for i := 0; i+1 < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], uint16(amplitude))
	}
	return frame
}

func TestBlockCutterCutsAtPauseAfterMinimum(t *testing.T) {
	cutter := &blockCutter{track: trackMicrophone}
	var blocks []meetingBlock
	for i := 0; i < 130; i++ {
		// Speech for the first 110 frames, then a pause.
		amp := int16(8000)
		if i >= 110 {
			amp = 0
		}
		if block, ok := cutter.add(tone(amp), int64(i)*meetingFrameMS); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 cut at the pause", len(blocks))
	}
	block := blocks[0]
	if block.startMS != 0 || block.endMS != 114*meetingFrameMS || !block.voiced {
		t.Errorf("block = %d-%dms voiced=%v, want 0-11400ms voiced", block.startMS, block.endMS, block.voiced)
	}
	if len(block.pcm) != 114*meetFrameBytes {
		t.Errorf("pcm length %d does not match the span", len(block.pcm))
	}
}

func TestBlockCutterHardLimitAndNoLostFrames(t *testing.T) {
	cutter := &blockCutter{track: trackOutput}
	total, cuts := 0, 0
	var lastEnd int64
	for i := 0; i < 700; i++ {
		if block, ok := cutter.add(tone(8000), int64(i)*meetingFrameMS); ok {
			cuts++
			total += len(block.pcm) / meetFrameBytes
			if block.startMS != lastEnd {
				t.Fatalf("block starts at %d, previous ended at %d: audio lost or repeated", block.startMS, lastEnd)
			}
			lastEnd = block.endMS
		}
	}
	if block, ok := cutter.flush(); ok {
		total += len(block.pcm) / meetFrameBytes
	}
	if cuts != 2 || total != 700 {
		t.Errorf("continuous speech: %d cuts, %d frames kept; want 2 cuts and all 700 frames", cuts, total)
	}
}

func TestBlockCutterMarksSilenceUnvoiced(t *testing.T) {
	cutter := &blockCutter{track: trackMicrophone}
	for i := 0; i < 50; i++ {
		cutter.add(tone(0), int64(i)*meetingFrameMS)
	}
	block, ok := cutter.flush()
	if !ok || block.voiced {
		t.Fatalf("silent block voiced=%v ok=%v; silence must not be uploaded", block.voiced, ok)
	}
}

func TestParseMicrophonesIgnoresOutputsAndMonitors(t *testing.T) {
	dump := `[
	 {"type":"PipeWire:Interface:Node","info":{"state":"suspended","props":{"media.class":"Audio/Source","node.name":"alsa_input.mic","node.description":"Micrófono interno","object.serial":2152}}},
	 {"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"media.class":"Audio/Sink","node.name":"alsa_output.speakers","object.serial":77}}},
	 {"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"media.class":"Stream/Output/Audio","application.name":"Chrome","node.name":"chrome","object.serial":501}}},
	 {"type":"PipeWire:Interface:Node","info":{"state":"idle","props":{"media.class":"Audio/Source/Virtual","node.name":"virtual","object.serial":9}}}
	]`
	got, err := parseMicrophones([]byte(dump))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v; want exactly the real microphone", got, err)
	}
	if got[0].Target != "2152" || got[0].App != "alsa_input.mic" || got[0].Label != "Micrófono interno" || got[0].Kind != kindInput {
		t.Errorf("unexpected microphone %+v", got[0])
	}
	outputs, _ := parseAudioSources([]byte(dump))
	if len(outputs) != 1 || outputs[0].Kind != kindOutput {
		t.Errorf("outputs = %+v, want only the browser stream, tagged output", outputs)
	}
}

func testMeetingApp(t *testing.T) (*App, *store) {
	t.Helper()
	database, err := openStoreAt(filepath.Join(t.TempDir(), "sonara.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	a := NewApp()
	a.store = database
	return a, database
}

func testRun(t *testing.T, a *App, database *store) *meetingRun {
	t.Helper()
	if err := database.saveSession("m1", meetingMode, "es"); err != nil {
		t.Fatal(err)
	}
	run := &meetingRun{
		sessionID: "m1", started: time.Now(), language: "es", state: meetingRecording,
		tracks: map[string]string{}, lastEnd: map[string]int64{}, failedBlock: map[int64]meetingBlock{},
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

	a.processBlock(run, meetingBlock{track: trackMicrophone, startMS: 12000, endMS: 20000, pcm: tone(8000), voiced: true})

	segments, _ := database.transcriptSegments("m1")
	if len(segments) != 1 || segments[0].Text != "Buenos días" || segments[0].Status != segmentOK || segments[0].StartMS != 12000 {
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
	a.processBlock(run, meetingBlock{track: trackOutput, pcm: tone(8000), voiced: true})
	if segments, _ := database.transcriptSegments("m1"); len(segments) != 0 {
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
	a.processBlock(run, meetingBlock{track: trackOutput, startMS: 5000, endMS: 9000, pcm: tone(8000), voiced: true})
	if calls != meetingRetries+1 {
		t.Errorf("tried %d times, want %d", calls, meetingRetries+1)
	}
	segments, _ := database.transcriptSegments("m1")
	if len(segments) != 1 || segments[0].Status != segmentFailed || run.failed != 1 {
		t.Fatalf("a failed block must leave a visible failed row: %+v failed=%d", segments, run.failed)
	}

	a.meetingTranscribe = func(context.Context, []byte, string) (string, error) { return "recuperado", nil }
	n, err := a.RetryMeetingSegments("m1")
	if err != nil || n != 1 {
		t.Fatalf("retry = %d, %v", n, err)
	}
	segments, _ = database.transcriptSegments("m1")
	if segments[0].Status != segmentOK || segments[0].Text != "recuperado" || run.failed != 0 {
		t.Errorf("after retry: %+v failed=%d", segments, run.failed)
	}
	if _, err := a.RetryMeetingSegments("other"); err == nil {
		t.Error("retry for a meeting whose audio is gone must say so")
	}
}

func TestEnqueueBlockOverflowRecordsGapAndSkipsSilence(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	run.jobs = make(chan meetingBlock, 1)

	a.enqueueBlock(run, meetingBlock{track: trackMicrophone, startMS: 0, endMS: 1000, voiced: false})
	if len(run.jobs) != 0 {
		t.Fatal("a silent block must not be queued")
	}
	a.enqueueBlock(run, meetingBlock{track: trackMicrophone, startMS: 0, endMS: 1000, voiced: true})
	a.enqueueBlock(run, meetingBlock{track: trackMicrophone, startMS: 1000, endMS: 2000, voiced: true})
	segments, _ := database.transcriptSegments("m1")
	if len(segments) != 1 || segments[0].Status != segmentGap || segments[0].StartMS != 1000 {
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
	database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 16000, EndMS: 20000, Text: "Sí, comparto pantalla.", Status: segmentOK})
	database.insertSegment("m1", TranscriptSegment{Track: trackMicrophone, StartMS: 12000, EndMS: 15000, Text: "Buenos días.", Status: segmentOK})
	database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 30000, EndMS: 40000, Status: segmentGap})

	view, err := a.MeetingTranscriptSegments("m1")
	if err != nil || len(view.Segments) != 3 || view.Segments[0].Text != "Buenos días." {
		t.Fatalf("segments must come back in time order: %+v, %v", view.Segments, err)
	}
	sessions, _ := database.recentSessions(10)
	if len(sessions) != 1 || sessions[0].Mode != meetingMode || sessions[0].Messages != 2 || sessions[0].Preview != "Buenos días." {
		t.Errorf("summary = %+v, want 2 spoken segments and the first as preview", sessions)
	}
	if _, err := a.MeetingTranscriptSegments("missing"); err == nil {
		t.Error("unknown session must error")
	}
	database.saveSession("copilot", "interview", "es")
	if _, err := a.MeetingTranscriptSegments("copilot"); err == nil {
		t.Error("a copilot session is not a meeting")
	}
}

func TestDeletingMeetingCascadesAndBlocksWhileRecording(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	database.insertSegment("m1", TranscriptSegment{Track: trackMicrophone, Text: "x", Status: segmentOK})
	database.saveSpeakerName("m1", trackMicrophone, "", "Walter")
	if _, err := a.DeleteSession("m1"); err == nil {
		t.Fatal("a meeting being recorded must not be deletable")
	}
	run.state = meetingIdle
	if _, err := a.DeleteSession("m1"); err != nil {
		t.Fatal(err)
	}
	var segments, speakers int
	database.db.QueryRow("SELECT COUNT(*) FROM transcript_segments").Scan(&segments)
	database.db.QueryRow("SELECT COUNT(*) FROM transcript_speakers").Scan(&speakers)
	if segments != 0 || speakers != 0 {
		t.Errorf("rows left behind: %d segments, %d speakers", segments, speakers)
	}
}

func TestRenameSpeaker(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	names, err := a.RenameMeetingSpeaker("m1", trackOutput, "", "  Ana ")
	if err != nil || names[trackOutput] != "Ana" || names[trackMicrophone] != "Yo" {
		t.Fatalf("names = %v, %v", names, err)
	}
	names, _ = a.RenameMeetingSpeaker("m1", trackOutput, "", "")
	if names[trackOutput] != "Participante" {
		t.Errorf("empty name must restore the default, got %q", names[trackOutput])
	}
	if _, err := a.RenameMeetingSpeaker("m1", "bogus", "", "x"); err == nil {
		t.Error("unknown track accepted")
	}
}

func TestFormatMeetingTranscript(t *testing.T) {
	started := time.Date(2026, 11, 16, 10, 30, 0, 0, time.UTC)
	got := formatMeetingTranscript(started, "es", []TranscriptSegment{
		{Track: trackMicrophone, StartMS: 12000, Text: "Buenos días. ¿Empezamos?", Status: segmentOK},
		{Track: trackOutput, StartMS: 16000, Text: "Sí, comparto pantalla.", Status: segmentOK},
		{Track: trackOutput, StartMS: 3_725_000, EndMS: 3_745_000, Status: segmentFailed},
		{Track: trackMicrophone, StartMS: 3_800_000, EndMS: 3_810_000, Status: segmentGap},
	}, map[string]string{trackMicrophone: "Yo", trackOutput: "Ana"})
	want := "Reunión: 2026-11-16 10:30\nIdioma: español\n\n" +
		"[00:00:12] Yo: Buenos días. ¿Empezamos?\n" +
		"[00:00:16] Ana: Sí, comparto pantalla.\n" +
		"[01:02:05–01:02:25] (sin transcribir: falló la transcripción, Ana)\n" +
		"[01:03:20–01:03:30] (sin transcribir: audio no capturado, Yo)\n"
	if got != want {
		t.Errorf("export mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestExportMeetingWritesAtomicallyAndAppendsExtension(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	database.insertSegment("m1", TranscriptSegment{Track: trackMicrophone, StartMS: 1000, EndMS: 2000, Text: "Hola", Status: segmentOK})
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

func TestMigration0004KeepsExistingSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sonara.db")
	first, err := openStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	first.saveSession("old", "interview", "es")
	first.appendMessage("old", "user", "pregunta")
	first.Close()
	second, err := openStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	sessions, _ := second.recentSessions(10)
	if len(sessions) != 1 || sessions[0].Messages != 1 || sessions[0].Preview != "pregunta" {
		t.Errorf("copilot session summary changed: %+v", sessions)
	}
	if _, err := second.insertSegment("old", TranscriptSegment{Track: "bogus", Status: segmentOK}); err == nil {
		t.Error("the schema must reject an unknown track")
	}
}

func TestTrailingGapsCoverTracksThatNeverCaptured(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	run.started = time.Now().Add(-10 * time.Second)
	run.lastEnd[trackMicrophone] = 9900 // captured until just before the stop
	a.recordTrailingGaps(run)
	segments, _ := database.transcriptSegments("m1")
	if len(segments) != 1 || segments[0].Track != trackOutput || segments[0].Status != segmentGap || segments[0].StartMS != 0 {
		t.Fatalf("only the track that never captured needs a gap: %+v", segments)
	}
}

// TestFindMicrophoneAppMatchesByStableNameNotSerial is a regression test for
// a real bug: SetMicrophone used to compare against AudioSource.Target (the
// PipeWire serial), but the UI's <option> sends AudioSource.App (the stable
// node name) — so the match never succeeded and every selection reported
// "el micrófono ya no está disponible" even with the microphone present.
func TestFindMicrophoneAppMatchesByStableNameNotSerial(t *testing.T) {
	sources := []AudioSource{
		{Target: "2152", App: "alsa_input.pci-0000_00_1f.3.analog-stereo", Label: "Micrófono interno"},
	}
	got, err := findMicrophoneApp(sources, "alsa_input.pci-0000_00_1f.3.analog-stereo")
	if err != nil || got != "alsa_input.pci-0000_00_1f.3.analog-stereo" {
		t.Fatalf("matching by App (the UI's actual value) failed: got %q, %v", got, err)
	}
	if _, err := findMicrophoneApp(sources, "2152"); err == nil {
		t.Fatal("the ephemeral serial must never match: it is not what the UI sends")
	}
	if _, err := findMicrophoneApp(sources, "unplugged-mic"); err == nil {
		t.Fatal("a microphone no longer present must error")
	}
}

func TestSetMicrophoneEndToEnd(t *testing.T) {
	a, database := testMeetingApp(t)
	a.microphonesHook = func(context.Context) ([]AudioSource, error) {
		return []AudioSource{{Target: "2152", App: "alsa_input.mic", Label: "Mic"}}, nil
	}
	got, err := a.SetMicrophone("alsa_input.mic")
	if err != nil || got != "alsa_input.mic" {
		t.Fatalf("got %q, %v", got, err)
	}
	if a.SelectedMicrophone() != "alsa_input.mic" {
		t.Errorf("SelectedMicrophone() = %q", a.SelectedMicrophone())
	}
	stored := database.setting(settingMicrophone)
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
