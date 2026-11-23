package app

import (
	"context"
	"strings"
	"time"

	"sonara/internal/storage"
	"sonara/internal/strutil"
)

// Conversation memory with a fixed size, however long the session runs: the
// last few minutes verbatim (sessionHistory), and everything older folded into
// a short running summary by the fast model, off the reply path. A one-hour
// meeting costs the same per reply as a five-minute one.
const (
	memoryWindowChars      = 2500 // recent turns kept word for word
	memoryMinWindowEntries = 4    // never fewer, even if they are long
	memorySummarizeAt      = 3000 // older text waiting before it is summarized
	memorySummaryChars     = 1500
	memoryContextChars     = 6000
)

// rememberLocked adds one turn to the conversation memory and moves what no
// longer fits the window to the summary queue. Caller holds a.mu.
func (a *App) rememberLocked(role, text string) {
	a.sessionHistory = append(a.sessionHistory, storage.Message{Role: role, Text: text})
	a.trimMemoryLocked()
}

func (a *App) trimMemoryLocked() {
	window := 0
	for _, message := range a.sessionHistory {
		window += len(message.Text)
	}
	for window > memoryWindowChars && len(a.sessionHistory) > memoryMinWindowEntries {
		oldest := a.sessionHistory[0]
		a.sessionHistory = a.sessionHistory[1:]
		a.memoryPending = append(a.memoryPending, oldest)
		window -= len(oldest.Text)
	}
	pending := 0
	for _, message := range a.memoryPending {
		pending += len(message.Text)
	}
	if pending < memorySummarizeAt || a.memorySummarizing || a.backend == nil {
		return
	}
	a.memorySummarizing = true
	batch := append([]storage.Message(nil), a.memoryPending...)
	go a.summarizeMemory(a.sessionGeneration, a.assistantMode, a.memorySummary, batch)
}

func (a *App) resetMemoryLocked() {
	a.memorySummary, a.memoryPending = "", nil
}

func (a *App) summarizeMemory(generation uint64, mode, previous string, batch []storage.Message) {
	var prompt strings.Builder
	if previous != "" {
		prompt.WriteString("Resumen anterior:\n" + previous + "\n\n")
	}
	prompt.WriteString("Turnos nuevos:\n")
	for _, message := range batch {
		prompt.WriteString(memoryRole(mode, message.Role) + ": " + message.Text + "\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	output, err := a.backend.Respond(ctx, prompt.String(), "memory", "")

	a.mu.Lock()
	defer a.mu.Unlock()
	a.memorySummarizing = false
	if a.sessionGeneration != generation {
		return
	}
	if err != nil {
		logf("conversation summary failed (will retry with the next turns): %v", err)
		return
	}
	a.memorySummary = strutil.Truncate(output.Text, memorySummaryChars)
	// batch was a prefix of memoryPending; newer evictions stay queued.
	a.memoryPending = a.memoryPending[min(len(batch), len(a.memoryPending)):]
	logf("conversation summary updated: %d turns folded in, %d chars", len(batch), len(a.memorySummary))
}

// memoryRole names who spoke for the model. In practice the stored "user"
// turns are the candidate's own spoken answers, so they are "me" there too:
// the summary keeps them under "Lo que dije" and the interviewer builds on them.
func memoryRole(mode, role string) string {
	practice := mode == "practice" || mode == "practice-turn"
	switch {
	case role == "me", practice && role == "user":
		return "me (what I said out loud)"
	case practice && role == "assistant":
		return "interviewer"
	}
	return role
}

// truncateKeepingEnd keeps the newest text: a long history must lose its
// oldest lines, never the turn being answered.
func truncateKeepingEnd(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	cut := len(value) - limit
	if newline := strings.IndexByte(value[cut:], '\n'); newline >= 0 {
		cut += newline + 1
	}
	return "[…]\n" + value[cut:]
}

// sessionReopenMessages is how much of a saved session is reloaded: the older
// part is folded into the summary right away.
const sessionReopenMessages = 60
