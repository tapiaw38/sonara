package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// store is Sonara's only durable state: the API key and the interview sessions.
// Everything else (audio, captures, window state) is disposable by design.
//
// The key is kept here in plain text under file mode 0600, which was a deliberate
// choice over the OS keyring: anything running as this user, and any backup that
// copies the file, can read it. Move it to the Secret Service if that stops being
// acceptable; nothing else in this file has to change.
type store struct {
	db   *sql.DB
	path string
}

const (
	// settingAPIKey is the settings row holding the OpenAI key.
	settingAPIKey = "openai_api_key"
	// settingAudioApp remembers which application owns the meeting audio. The
	// PipeWire serial is useless across restarts because the node is recreated,
	// but the application is stable, so this is what makes the choice stick.
	settingAudioApp      = "preferred_audio_app"
	settingUIPreferences = "ui_preferences"
	// settingDeepSeekKey is the DeepSeek key, stored like the OpenAI one.
	settingDeepSeekKey = "deepseek_api_key"
	// settingMicrophone remembers the microphone by PipeWire node name: the
	// serial is recreated on every boot, the name is stable.
	settingMicrophone = "preferred_microphone"
	// settingGeminiKey is the Gemini key used for phase-2 diarization.
	settingGeminiKey = "gemini_api_key"
	// settingAzureOpenAI holds the whole Azure OpenAI form as one JSON blob
	// (key, endpoint, API version, both deployment names): one save action
	// in Settings, one row here.
	settingAzureOpenAI = "azure_openai_config"
	// settingProviders holds which named provider (openai/deepseek/azure)
	// serves each capability — an explicit choice, never an automatic
	// fallback chain. See the providers package.
	settingProviders = "capability_providers"
)

func storePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "sonara")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "sonara.db"), nil
}

func openStore() (*store, error) {
	path, err := storePath()
	if err != nil {
		return nil, err
	}
	return openStoreAt(path)
}

func openStoreAt(path string) (*store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One desktop process, so a single connection avoids SQLITE_BUSY entirely.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	// Schema lives in migrations/*.sql, applied here on every start so an
	// installed app upgrades the database it already has.
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	// SQLite creates the file with the process umask, which is world readable on
	// most systems. The key lives in here, so tighten it every open: the file
	// may predate this code or have been restored from a backup.
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db, path: path}, nil
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) setting(key string) string {
	var value string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value); err != nil {
		return ""
	}
	return value
}

func (s *store) setSetting(key, value string) error {
	if value == "" {
		_, err := s.db.Exec("DELETE FROM settings WHERE key = ?", key)
		return err
	}
	_, err := s.db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	return err
}

// UIPreferences stores persistent layout choices in the settings table.
type UIPreferences struct {
	ResponsePanelHeight   int `json:"responsePanelHeight"`
	TranscriptPanelHeight int `json:"transcriptPanelHeight"`
}

func normalizeUIPreferences(preferences UIPreferences) UIPreferences {
	preferences.ResponsePanelHeight = clampPreference(preferences.ResponsePanelHeight, 280, 2400)
	preferences.TranscriptPanelHeight = clampPreference(preferences.TranscriptPanelHeight, 180, 2400)
	return preferences
}

func clampPreference(value, minValue, maxValue int) int {
	if value == 0 {
		return 0
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func (s *store) uiPreferences() (UIPreferences, error) {
	encoded := s.setting(settingUIPreferences)
	if encoded == "" {
		return UIPreferences{}, nil
	}
	var preferences UIPreferences
	if err := json.Unmarshal([]byte(encoded), &preferences); err != nil {
		return UIPreferences{}, fmt.Errorf("preferencias de interfaz inválidas: %w", err)
	}
	return normalizeUIPreferences(preferences), nil
}

func (s *store) saveUIPreferences(preferences UIPreferences) error {
	encoded, err := json.Marshal(normalizeUIPreferences(preferences))
	if err != nil {
		return err
	}
	return s.setSetting(settingUIPreferences, string(encoded))
}

// AzureOpenAISettings is the whole Azure OpenAI form, saved and loaded as one
// unit. APIKey is never read back by the UI (same policy as the OpenAI key);
// it is only ever written here.
type AzureOpenAISettings struct {
	APIKey   string `json:"apiKey"`
	Endpoint string `json:"endpoint"`
	// Chat and transcription are versioned independently on Azure's own
	// schedule; a resource using both routinely runs two different values at
	// once (confirmed against a real Azure resource that does exactly this).
	ChatAPIVersion       string `json:"chatApiVersion"`
	TranscribeAPIVersion string `json:"transcribeApiVersion"`
	ChatDeployment       string `json:"chatDeployment"`
	TranscribeDeployment string `json:"transcribeDeployment"`
}

func (s *store) azureOpenAISettings() (AzureOpenAISettings, error) {
	encoded := s.setting(settingAzureOpenAI)
	if encoded == "" {
		return AzureOpenAISettings{}, nil
	}
	var settings AzureOpenAISettings
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return AzureOpenAISettings{}, fmt.Errorf("configuración de Azure OpenAI inválida: %w", err)
	}
	return settings, nil
}

func (s *store) saveAzureOpenAISettings(settings AzureOpenAISettings) error {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.setSetting(settingAzureOpenAI, string(encoded))
}

// CapabilityProviders is which named provider serves each capability — one
// explicit choice each, no fallback chain. Empty fields default to "openai".
type CapabilityProviders struct {
	Text            string `json:"text"`
	Vision          string `json:"vision"`
	LiveTranscribe  string `json:"liveTranscribe"`
	MeetingProvider string `json:"meeting"`
	// LiveMode is how the copilot transcribes: "batch" (default) or "stream".
	LiveMode string `json:"liveMode"`
}

func (s *store) capabilityProviders() (CapabilityProviders, error) {
	encoded := s.setting(settingProviders)
	if encoded == "" {
		return CapabilityProviders{}, nil
	}
	var providers CapabilityProviders
	if err := json.Unmarshal([]byte(encoded), &providers); err != nil {
		return CapabilityProviders{}, fmt.Errorf("selección de proveedores inválida: %w", err)
	}
	return providers, nil
}

func (s *store) saveCapabilityProviders(providers CapabilityProviders) error {
	encoded, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	return s.setSetting(settingProviders, string(encoded))
}

// saveSession records a session before its first message, so a session that
// never got an answer still shows up in the list.
func (s *store) saveSession(id, mode, language string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, started_at, mode, language) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, language = excluded.language`,
		id, time.Now().UTC().Format(time.RFC3339), mode, language)
	return err
}

func (s *store) appendMessage(sessionID, role, text string) error {
	_, err := s.db.Exec(
		"INSERT INTO messages (session_id, role, text, created_at) VALUES (?, ?, ?, ?)",
		sessionID, role, text, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *store) saveVisualContext(sessionID, text string) error {
	_, err := s.db.Exec("UPDATE sessions SET visual_context = ? WHERE id = ?", text, sessionID)
	return err
}

// SessionProfile is interview-specific context saved with its session.
type SessionProfile struct {
	Scenario  string `json:"scenario"`
	Company   string `json:"company"`
	Role      string `json:"role"`
	Objective string `json:"objective"`
	Notes     string `json:"notes"`
}

func normalizeSessionProfile(profile SessionProfile) SessionProfile {
	profile.Scenario = truncateContext(strings.TrimSpace(profile.Scenario), 120)
	if profile.Scenario == "" {
		profile.Scenario = "General"
	}
	profile.Company = truncateContext(strings.TrimSpace(profile.Company), 160)
	profile.Role = truncateContext(strings.TrimSpace(profile.Role), 160)
	profile.Objective = truncateContext(strings.TrimSpace(profile.Objective), 400)
	profile.Notes = truncateContext(strings.TrimSpace(profile.Notes), 1200)
	return profile
}

func (profile SessionProfile) promptContext() string {
	profile = normalizeSessionProfile(profile)
	var parts []string
	if profile.Scenario != "" {
		parts = append(parts, "Interview scenario: "+profile.Scenario)
	}
	if profile.Company != "" {
		parts = append(parts, "Company: "+profile.Company)
	}
	if profile.Role != "" {
		parts = append(parts, "Role: "+profile.Role)
	}
	if profile.Objective != "" {
		parts = append(parts, "Interview goal: "+profile.Objective)
	}
	if profile.Notes != "" {
		parts = append(parts, "Candidate background and instructions: "+profile.Notes)
	}
	return strings.Join(parts, "\n")
}

func (s *store) saveSessionProfile(sessionID string, profile SessionProfile) error {
	encoded, err := json.Marshal(normalizeSessionProfile(profile))
	if err != nil {
		return err
	}
	result, err := s.db.Exec("UPDATE sessions SET session_profile = ? WHERE id = ?", string(encoded), sessionID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("sesión %s no existe", sessionID)
	}
	return nil
}

func (s *store) sessionProfile(sessionID string) (SessionProfile, error) {
	var encoded string
	if err := s.db.QueryRow("SELECT session_profile FROM sessions WHERE id = ?", sessionID).Scan(&encoded); err != nil {
		return SessionProfile{}, err
	}
	var profile SessionProfile
	if err := json.Unmarshal([]byte(encoded), &profile); err != nil {
		return SessionProfile{}, fmt.Errorf("sesión %s tiene contexto inválido: %w", sessionID, err)
	}
	return normalizeSessionProfile(profile), nil
}

// loadMessages returns the last limit messages in the order they were spoken.
func (s *store) loadMessages(sessionID string, limit int) ([]sessionMessage, error) {
	rows, err := s.db.Query(
		"SELECT role, text FROM (SELECT id, role, text FROM messages WHERE session_id = ? ORDER BY id DESC LIMIT ?) ORDER BY id ASC",
		sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []sessionMessage
	for rows.Next() {
		var message sessionMessage
		if err := rows.Scan(&message.role, &message.text); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *store) visualContext(sessionID string) string {
	var text string
	if err := s.db.QueryRow("SELECT visual_context FROM sessions WHERE id = ?", sessionID).Scan(&text); err != nil {
		return ""
	}
	return text
}

// SessionSummary is one row of the session list the UI shows.
type SessionSummary struct {
	ID        string `json:"id"`
	StartedAt string `json:"startedAt"`
	Mode      string `json:"mode"`
	Language  string `json:"language"`
	Messages  int    `json:"messages"`
	Preview   string `json:"preview"`
	Title     string `json:"title"`
}

// SessionViewMessage is a saved message shown when a session is reopened.
type SessionViewMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

type OpenSessionResult struct {
	Sessions []SessionSummary     `json:"sessions"`
	Messages []SessionViewMessage `json:"messages"`
}

func (s *store) sessionMessages(sessionID string) ([]SessionViewMessage, error) {
	rows, err := s.db.Query(
		"SELECT role, text, created_at FROM messages WHERE session_id = ? ORDER BY id ASC",
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []SessionViewMessage{}
	for rows.Next() {
		var message SessionViewMessage
		if err := rows.Scan(&message.Role, &message.Text, &message.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *store) recentSessions(limit int) ([]SessionSummary, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.started_at, s.mode, s.language, s.title,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)
		         + (SELECT COUNT(*) FROM transcript_segments t WHERE t.session_id = s.id AND t.status = 'ok'),
		       COALESCE((SELECT m.text FROM messages m WHERE m.session_id = s.id AND m.role = 'user' ORDER BY m.id ASC LIMIT 1),
		                (SELECT t.text FROM transcript_segments t WHERE t.session_id = s.id AND t.status = 'ok' ORDER BY t.start_ms, t.id LIMIT 1), '')
		FROM sessions s ORDER BY s.started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]SessionSummary, 0, limit)
	for rows.Next() {
		var item SessionSummary
		if err := rows.Scan(&item.ID, &item.StartedAt, &item.Mode, &item.Language, &item.Title, &item.Messages, &item.Preview); err != nil {
			return nil, err
		}
		item.Preview = truncateContext(item.Preview, 120)
		sessions = append(sessions, item)
	}
	return sessions, rows.Err()
}

func (s *store) saveTitle(sessionID, title string) error {
	_, err := s.db.Exec("UPDATE sessions SET title = ? WHERE id = ?", title, sessionID)
	return err
}

// needsTitle reports whether a session is still unnamed. Titling costs a model
// call, so it happens once and only once per session.
func (s *store) needsTitle(sessionID string) bool {
	var title string
	if err := s.db.QueryRow("SELECT title FROM sessions WHERE id = ?", sessionID).Scan(&title); err != nil {
		return false
	}
	return title == ""
}

// Sessions lists the stored sessions, newest first, for the session picker.
func (a *App) Sessions() ([]SessionSummary, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return []SessionSummary{}, nil
	}
	return database.recentSessions(50)
}

// OpenSession reloads a stored session: its history becomes the prompt context
// again, so the assistant picks up where that interview left off.
func (a *App) OpenSession(id string) (OpenSessionResult, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return OpenSessionResult{}, fmt.Errorf("sin persistencia disponible")
	}
	viewMessages, err := database.sessionMessages(id)
	if err != nil {
		return OpenSessionResult{}, err
	}
	messages := make([]sessionMessage, 0, sessionHistoryLimit)
	for _, message := range viewMessages[max(0, len(viewMessages)-sessionHistoryLimit):] {
		messages = append(messages, sessionMessage{role: message.Role, text: message.Text})
	}
	visual := database.visualContext(id)
	profile, err := database.sessionProfile(id)
	if err != nil {
		return OpenSessionResult{}, err
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
	a.sessionProfile = profile
	a.latestVisualContext = visual
	a.pendingAudio = nil
	a.transcript = "Sesión reabierta"
	a.translation, a.voiceReply = "", ""
	a.voiceSequence++
	a.streaming = false
	a.droppedSegments = 0
	a.listenStatus = fmt.Sprintf("Sesión reabierta (%d mensajes)", len(messages))
	a.mu.Unlock()
	logf("reopened session %s with %d messages", id, len(messages))
	sessions, err := a.Sessions()
	if err != nil {
		return OpenSessionResult{}, err
	}
	return OpenSessionResult{Sessions: sessions, Messages: viewMessages}, nil
}

// DeleteSession removes a stored session. The one in progress cannot be
// deleted, since its rows are still being written.
func (a *App) DeleteSession(id string) ([]SessionSummary, error) {
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
	if err := database.deleteSession(id); err != nil {
		return nil, err
	}
	return a.Sessions()
}

func (s *store) deleteSession(id string) error {
	result, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("sesión %s no existe", id)
	}
	return nil
}
