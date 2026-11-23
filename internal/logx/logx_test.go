package logx

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureLog points the logger at a buffer so tests never write to the real
// cache directory, and resets the dedup state between cases.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var sink bytes.Buffer
	logOnce.Do(func() {}) // keep openLog from touching the real cache directory
	previousLogger, previousDedup, previousWindow := logger, dedup, logRepeatWindow
	logger, dedup = log.New(&sink, "", 0), nil
	t.Cleanup(func() { logger, dedup, logRepeatWindow = previousLogger, previousDedup, previousWindow })
	return &sink
}

// The reconnect loop retries every two seconds for as long as listening is on.
// One line per tick would bury real errors and then push the file past its size
// limit, discarding them outright.
func TestLogDedupCollapsesRepeats(t *testing.T) {
	sink := captureLog(t)

	logRepeatWindow = time.Hour // isolate collapsing from the periodic report

	for range 5 {
		Dedup("audio-target", "no audio target: %s", "ambiguous")
	}
	if got := countLines(sink); got != 1 {
		t.Errorf("5 identical retries wrote %d lines, want 1: %q", got, sink.String())
	}

	// A change of state emits the held count for the old line plus the new one,
	// and going back emits again: collapsing those would hide the transitions.
	Dedup("audio-target", "capturing PipeWire target %s", "936")
	if got := countLines(sink); got != 3 {
		t.Errorf("the transition wrote %d lines, want 3 (held count + new state)", got)
	}
	Dedup("audio-target", "no audio target: %s", "ambiguous")
	if got := countLines(sink); got != 4 {
		t.Errorf("the recurrence wrote %d lines, want 4", got)
	}

	// Keys are independent, so one noisy loop cannot mute another.
	Dedup("recorder", "no audio target: %s", "ambiguous")
	if got := countLines(sink); got != 5 {
		t.Errorf("a separate key wrote %d lines, want 5", got)
	}
}

func countLines(sink *bytes.Buffer) int {
	trimmed := strings.TrimSpace(sink.String())
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

// Suppressing a repeating failure outright made a recorder that was dying and
// respawning look like an idle log, which cost a wrong diagnosis. The count has
// to surface on its own, without waiting for the state to change.
func TestLogDedupReportsTheRepeatCount(t *testing.T) {
	sink := captureLog(t)
	logRepeatWindow = 0 // every repeat is past the window

	for range 3 {
		Dedup("recorder", "recorder on target %s stopped", "936")
	}
	out := sink.String()
	if got := strings.Count(out, "recorder on target"); got != 3 {
		t.Errorf("wrote %d lines with a zero window, want all 3: %q", got, out)
	}
	if !strings.Contains(out, "×1") {
		t.Errorf("repeat lines carry no count: %q", out)
	}
}

// Repeats held back before a transition must still be reported, or a loop that
// ran a hundred times and then recovered leaves no trace of having happened.
func TestLogDedupFlushesHeldRepeatsOnChange(t *testing.T) {
	sink := captureLog(t)
	logRepeatWindow = time.Hour // nothing escapes on the window path

	for range 4 {
		Dedup("recorder", "recorder on target %s stopped", "936")
	}
	Dedup("recorder", "capturing PipeWire target %s", "936")

	out := sink.String()
	if !strings.Contains(out, "repetido 3 veces más") {
		t.Errorf("held repeats were not flushed on the transition: %q", out)
	}
	if !strings.Contains(out, "capturing PipeWire target 936") {
		t.Errorf("the new state was not logged: %q", out)
	}
}

func TestOpenLogFileAppendsThenDiscardsWhenOversized(t *testing.T) {
	path := filepath.Join(t.TempDir(), logFileName)

	file, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	if _, err := file.WriteString("primera corrida\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	file.Close()

	// A second run has to keep what the first one recorded: the whole point is
	// surviving the crash that ended the previous session.
	file, err = openLogFile(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := file.WriteString("segunda corrida\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	file.Close()
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(kept) != "primera corrida\nsegunda corrida\n" {
		t.Errorf("log did not append across runs, got %q", kept)
	}

	// Past the limit it starts over, so the file cannot grow without bound.
	if err := os.WriteFile(path, make([]byte, logSizeLimit+1), 0o600); err != nil {
		t.Fatalf("grow: %v", err)
	}
	file, err = openLogFile(path)
	if err != nil {
		t.Fatalf("reopen oversized: %v", err)
	}
	file.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("oversized log kept %d bytes, want a fresh file", info.Size())
	}
}
