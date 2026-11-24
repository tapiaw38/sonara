package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"sonara/internal/storage"
	"sonara/internal/strutil"
)

// Sessions lists the stored sessions, newest first, for the session picker.
func (a *App) Sessions() ([]storage.SessionSummary, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return []storage.SessionSummary{}, nil
	}
	return database.RecentSessions(50)
}

// OpenSession reloads a stored session: its history becomes the prompt context
// again, so the assistant picks up where that interview left off.
func (a *App) OpenSession(id string) (storage.OpenSessionResult, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return storage.OpenSessionResult{}, fmt.Errorf("sin persistencia disponible")
	}
	viewMessages, err := database.SessionMessages(id)
	if err != nil {
		return storage.OpenSessionResult{}, err
	}
	messages := make([]storage.Message, 0, sessionReopenMessages)
	for _, message := range viewMessages[max(0, len(viewMessages)-sessionReopenMessages):] {
		messages = append(messages, storage.Message{Role: message.Role, Text: message.Text})
	}
	visual := database.VisualContext(id)
	profile, err := database.SessionProfile(id)
	if err != nil {
		return storage.OpenSessionResult{}, err
	}

	a.mu.Lock()
	// Reopening is a session switch, so in-flight audio and replies from the
	// previous one have to be abandoned exactly as NewSession does.
	a.voiceCancel()
	if a.replyCancel != nil {
		a.replyCancel()
		a.replyCancel = nil
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	a.voiceContext, a.voiceCancel = context.WithCancel(parent)
	a.sessionGeneration++
	a.replySequence++
	a.sessionID = id
	a.sessionHistory = messages
	a.resetMemoryLocked()
	a.trimMemoryLocked() // a long reopened session starts with its summary
	a.sessionProfile = profile
	a.latestVisualContext = visual
	a.pendingAudio = nil
	a.transcript = "Sesión reabierta"
	a.transcriptFinal = false
	a.translation, a.voiceReply = "", ""
	a.translationLines, a.voiceReplyLines = nil, nil
	a.voiceReplyTranslation = ""
	a.voiceSequence++
	a.streaming = false
	a.droppedSegments = 0
	a.listenStatus = fmt.Sprintf("Sesión reabierta (%d mensajes)", len(messages))
	a.mu.Unlock()
	logf("reopened session %s with %d messages", id, len(messages))
	sessions, err := a.Sessions()
	if err != nil {
		return storage.OpenSessionResult{}, err
	}
	return storage.OpenSessionResult{Sessions: sessions, Messages: viewMessages}, nil
}

// DeleteSession removes a stored session. The one in progress cannot be
// deleted, since its rows are still being written.
func (a *App) DeleteSession(id string) ([]storage.SessionSummary, error) {
	a.mu.RLock()
	database, current := a.store, a.sessionID
	a.mu.RUnlock()
	if database == nil {
		return nil, fmt.Errorf("sin persistencia disponible")
	}
	if id == current {
		return nil, fmt.Errorf("no se puede borrar la sesión en curso")
	}
	if a.meetingInProgress(id) {
		return nil, fmt.Errorf("no se puede borrar una reunión que se está grabando")
	}
	if err := database.DeleteSession(id); err != nil {
		return nil, err
	}
	return a.Sessions()
}

func (a *App) SetAssistantMode(mode string) string {
	switch mode {
	case "daily", "coding", "coaching", "practice":
	default:
		mode = "interview"
	}
	a.mu.Lock()
	a.assistantMode = mode
	a.mu.Unlock()
	return mode
}

// SaveSessionProfile stores interview-specific context alongside its session.
// The UI also uses this for one-time migration from legacy localStorage keys.
func (a *App) SaveSessionProfile(sessionID string, profile storage.SessionProfile) error {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return fmt.Errorf("sin persistencia disponible")
	}
	profile = storage.NormalizeSessionProfile(profile)
	if err := database.SaveSessionProfile(sessionID, profile); err != nil {
		return err
	}
	if encoded, err := json.Marshal(profile); err == nil {
		if err := database.SetSetting(storage.SettingLastProfile, string(encoded)); err != nil {
			logf("could not remember the interview context: %v", err)
		}
	}
	a.mu.Lock()
	if a.sessionID == sessionID {
		a.sessionProfile = profile
	}
	a.mu.Unlock()
	return nil
}

// inheritLastProfile gives a new session the context last saved, so it does
// not have to be typed again. A reopened session keeps its own.
func (a *App) inheritLastProfile(database *storage.Store, sessionID string) {
	raw := database.Setting(storage.SettingLastProfile)
	if raw == "" {
		return
	}
	var profile storage.SessionProfile
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		return
	}
	profile = storage.NormalizeSessionProfile(profile)
	if err := database.SaveSessionProfile(sessionID, profile); err != nil {
		logf("could not apply the last interview context to %s: %v", sessionID, err)
		return
	}
	a.mu.Lock()
	if a.sessionID == sessionID {
		a.sessionProfile = profile
	}
	a.mu.Unlock()
}

// GetSessionProfile loads interview context from SQLite for the selected session.
func (a *App) GetSessionProfile(sessionID string) (storage.SessionProfile, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return storage.SessionProfile{}, fmt.Errorf("sin persistencia disponible")
	}
	return database.SessionProfile(sessionID)
}

func (a *App) NewSession() bool {
	a.mu.Lock()
	a.voiceCancel()
	if a.replyCancel != nil {
		a.replyCancel()
		a.replyCancel = nil
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	a.voiceContext, a.voiceCancel = context.WithCancel(parent)
	a.sessionGeneration++
	a.replySequence++
	a.sessionID = newSessionID()
	a.sessionHistory = nil
	a.resetMemoryLocked()
	a.sessionProfile = storage.SessionProfile{}
	a.latestVisualContext = ""
	a.pendingAudio = nil
	a.transcript = "Esperando audio de la reunión…"
	a.transcriptFinal = false
	a.translation = ""
	a.translationLines = nil
	a.voiceReply = ""
	a.voiceReplyLines, a.voiceReplyTranslation = nil, ""
	a.voiceSequence++
	a.streaming = false
	a.droppedSegments = 0
	a.listenStatus = "Nueva sesión"
	database, sessionID, mode, language := a.store, a.sessionID, a.assistantMode, a.audioLanguage
	a.mu.Unlock()
	if database != nil {
		if language == "" {
			language = "es"
		}
		// Recorded up front so an session that never gets an answer still shows
		// up in the list rather than vanishing.
		if err := database.SaveSession(sessionID, mode, language); err != nil {
			logf("could not record session %s: %v", sessionID, err)
		}
		a.inheritLastProfile(database, sessionID)
	}
	return true
}

func (a *App) appendSessionMessageLocked(role, content string) {
	a.rememberLocked(role, content)
	// In memory we keep only recent turns plus a summary (copilot_memory.go);
	// the store keeps the whole session so it can be reopened later.
	// Save in conversation order before another session can be opened. Separate
	// goroutines here could reorder a question and its answer or miss a reopen.
	if database, id := a.store, a.sessionID; database != nil {
		opening := a.openingExchangeLocked()
		if err := database.AppendMessage(id, role, content); err != nil {
			logf("could not store a %s message for session %s: %v", role, id, err)
			return
		}
		// Naming uses the model, so only that work stays asynchronous.
		if role == "assistant" && opening != "" {
			go a.nameSession(database, id, opening)
		}
	}
}

// openingExchangeLocked returns the first question and answer of the session,
// or "" when there is not a full exchange yet.
func (a *App) openingExchangeLocked() string {
	if len(a.sessionHistory) < 2 {
		return ""
	}
	var exchange strings.Builder
	for _, message := range a.sessionHistory[:2] {
		exchange.WriteString(message.Role)
		exchange.WriteString(": ")
		exchange.WriteString(message.Text)
		exchange.WriteString("\n")
	}
	return strutil.Truncate(exchange.String(), 1200)
}

// nameSession asks the cheapest model for a short label. A failure is silent on
// purpose: an unnamed session still lists by date, so this is never worth an
// error in the user's face.
func (a *App) nameSession(database *storage.Store, sessionID, exchange string) {
	if !database.NeedsTitle(sessionID) {
		return
	}
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	if backend == nil {
		return
	}
	title, err := backend.Title(a.requestContext(), exchange)
	if err != nil || title == "" {
		logf("could not name session %s: %v", sessionID, err)
		return
	}
	if err := database.SaveTitle(sessionID, title); err != nil {
		logf("could not store the title for session %s: %v", sessionID, err)
		return
	}
	logf("session %s named %q", sessionID, title)
}

func (a *App) buildSessionContextLocked() string {
	// The profile is bounded on its own (normalizeSessionProfile) and stays
	// out of the shared budget: long notes must not crowd out the summary.
	var profile string
	if brief := a.sessionProfile.PromptContext(); strings.TrimSpace(brief) != "" {
		profile = "Interview profile and goals:\n" + brief + "\n\n"
	}
	var context strings.Builder
	if visual := strings.TrimSpace(a.latestVisualContext); visual != "" {
		context.WriteString("Recent screenshot analysis:\n")
		context.WriteString(visual)
		context.WriteString("\n\n")
	}
	if summary := strings.TrimSpace(a.memorySummary); summary != "" {
		context.WriteString("Conversation so far (summary of older turns):\n")
		context.WriteString(summary)
		context.WriteString("\n\n")
	}
	header := strutil.Truncate(context.String(), memoryContextChars/2)
	var recent strings.Builder
	for _, message := range a.sessionHistory {
		recent.WriteString(memoryRole(a.assistantMode, message.Role))
		recent.WriteString(": ")
		recent.WriteString(message.Text)
		recent.WriteString("\n")
	}
	return strings.TrimSpace(profile + header + "\n\n" + truncateKeepingEnd(recent.String(), memoryContextChars-len(header)))
}
