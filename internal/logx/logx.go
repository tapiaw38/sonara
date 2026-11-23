package logx

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Sonara is launched detached from a desktop entry, so stdout and stderr go to
// /dev/null and nothing survives a crash. These helpers write the error paths to
// a file under the cache directory instead, which is the only post-mortem there
// is: everything else is a status string that disappears with the window.
const (
	logFileName = "sonara.log"
	// ponytail: truncate at startup instead of rotating. A session writes a few
	// KB; swap in log rotation only if these logs ever need to outlive a run.
	logSizeLimit = 2 << 20
)

var (
	logOnce   sync.Once
	logger    *log.Logger
	logTarget string
)

func logPath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "sonara")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, logFileName), nil
}

// Logf records one line. It never fails the caller: a diagnostic that can break
// the app is worse than no diagnostic.
func Logf(format string, args ...any) {
	logOnce.Do(openLog)
	if logger == nil {
		return
	}
	logger.Output(2, fmt.Sprintf(format, args...))
}

// dedupState tracks what was last written under one key and how many identical
// lines have been held back since.
type dedupState struct {
	message    string
	suppressed int
	emittedAt  time.Time
}

var (
	lastMu sync.Mutex
	dedup  map[string]*dedupState
	// logRepeatWindow is how long identical lines are collapsed before the
	// count is reported. A var so tests do not have to wait it out.
	logRepeatWindow = 30 * time.Second
)

// Dedup collapses identical consecutive lines under the same key, but never
// hides them: the count is reported every logRepeatWindow, and again when the
// state finally changes.
//
// Plain suppression was a mistake worth not repeating. The reconnect loop
// retries every two seconds, and silencing it turned a recorder that was dying
// and respawning into a log that looked idle, which sent a diagnosis the wrong
// way. A loop that repeats forever is itself the signal.
func Dedup(key, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	now := time.Now()

	lastMu.Lock()
	if dedup == nil {
		dedup = make(map[string]*dedupState)
	}
	state, seen := dedup[key]
	if !seen {
		state = &dedupState{}
		dedup[key] = state
	}
	var held, emit string
	switch {
	case state.message != message:
		// The state changed, so report whatever was held back about the old one
		// before moving on; otherwise those repeats are lost for good.
		if state.suppressed > 0 {
			held = fmt.Sprintf("%s (repetido %d veces más en %s)",
				state.message, state.suppressed, now.Sub(state.emittedAt).Round(time.Second))
		}
		emit = message
		state.message, state.suppressed, state.emittedAt = message, 0, now
	case now.Sub(state.emittedAt) >= logRepeatWindow:
		state.suppressed++
		emit = fmt.Sprintf("%s (×%d en %s)", message, state.suppressed, now.Sub(state.emittedAt).Round(time.Second))
		state.suppressed, state.emittedAt = 0, now
	default:
		state.suppressed++
	}
	lastMu.Unlock()

	if held == "" && emit == "" {
		return
	}
	logOnce.Do(openLog)
	if logger == nil {
		return
	}
	// Depth 2 so the line points at whoever called Dedup, not at this file.
	if held != "" {
		logger.Output(2, held)
	}
	if emit != "" {
		logger.Output(2, emit)
	}
}

func openLog() {
	// logOnce is one global switch for the whole test binary: whichever test
	// calls Logf/Dedup first decides where it points, regardless of file
	// or declaration order. Without this check, any test exercising real app
	// logic (not just logging_test.go's own cases) could win that race and
	// send its lines into the user's real log file instead of a test's own
	// captured buffer.
	if testing.Testing() {
		logger = log.New(io.Discard, "", log.LstdFlags|log.Lshortfile)
		return
	}
	path, err := logPath()
	if err != nil {
		// Without a cache directory stderr is all there is, and it may well be
		// /dev/null; that is still better than dropping the line silently.
		logger = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
		return
	}
	file, err := openLogFile(path)
	if err != nil {
		logger = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)
		return
	}
	logger, logTarget = log.New(file, "", log.LstdFlags|log.Lshortfile), path
}

// openLogFile appends to the log, discarding it first once it outgrows the
// limit. A long-lived install would otherwise grow this file without bound.
func openLogFile(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > logSizeLimit {
		os.Remove(path)
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
