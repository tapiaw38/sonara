package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"sonara/internal/strutil"
)

// Store is Sonara's only durable state: the API key and the interview sessions.
// Everything else (audio, captures, window state) is disposable by design.
//
// The key is kept here in plain text under file mode 0600, which was a deliberate
// choice over the OS keyring: anything running as this user, and any backup that
// copies the file, can read it. Move it to the Secret Service if that stops being
// acceptable; nothing else in this file has to change.
type Store struct {
	db   *sql.DB
	path string
}

const (
	// SettingAPIKey is the settings row holding the OpenAI key.
	SettingAPIKey = "openai_api_key"
	// SettingAudioApp remembers which application owns the meeting audio. The
	// PipeWire serial is useless across restarts because the node is recreated,
	// but the application is stable, so this is what makes the choice stick.
	SettingAudioApp      = "preferred_audio_app"
	SettingUIPreferences = "ui_preferences"
	// SettingDeepSeekKey is the DeepSeek key, stored like the OpenAI one.
	SettingDeepSeekKey = "deepseek_api_key"
	// SettingMicrophone remembers the microphone by PipeWire node name: the
	// serial is recreated on every boot, the name is stable.
	SettingMicrophone = "preferred_microphone"
	// SettingMeetingPeople is how many people share the microphone (0 = let
	// the separation guess).
	SettingMeetingPeople = "meeting_people"
	// SettingLastProfile is the interview context last saved, which every new
	// session starts with.
	SettingLastProfile = "last_session_profile"
	// SettingGeminiKey is the Gemini key used for phase-2 diarization.
	SettingGeminiKey = "gemini_api_key"
	// SettingAzureOpenAI holds the whole Azure OpenAI form as one JSON blob
	// (key, endpoint, API version, both deployment names): one save action
	// in Settings, one row here.
	SettingAzureOpenAI = "azure_openai_config"
	// SettingProviders holds which named provider (openai/deepseek/azure)
	// serves each capability — an explicit choice, never an automatic
	// fallback chain. See the providers package.
	SettingProviders = "capability_providers"
)

func Path() (string, error) {
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

func Open() (*Store, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return OpenAt(path)
}

func OpenAt(path string) (*Store, error) {
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
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Setting(key string) string {
	var value string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value); err != nil {
		return ""
	}
	return value
}

func (s *Store) SetSetting(key, value string) error {
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

func (s *Store) UIPreferences() (UIPreferences, error) {
	encoded := s.Setting(SettingUIPreferences)
	if encoded == "" {
		return UIPreferences{}, nil
	}
	var preferences UIPreferences
	if err := json.Unmarshal([]byte(encoded), &preferences); err != nil {
		return UIPreferences{}, fmt.Errorf("preferencias de interfaz inválidas: %w", err)
	}
	return normalizeUIPreferences(preferences), nil
}

func (s *Store) SaveUIPreferences(preferences UIPreferences) error {
	encoded, err := json.Marshal(normalizeUIPreferences(preferences))
	if err != nil {
		return err
	}
	return s.SetSetting(SettingUIPreferences, string(encoded))
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
	LiveDeployment       string `json:"liveDeployment"`
}

func (s *Store) AzureOpenAISettings() (AzureOpenAISettings, error) {
	encoded := s.Setting(SettingAzureOpenAI)
	if encoded == "" {
		return AzureOpenAISettings{}, nil
	}
	var settings AzureOpenAISettings
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return AzureOpenAISettings{}, fmt.Errorf("configuración de Azure OpenAI inválida: %w", err)
	}
	return settings, nil
}

func (s *Store) SaveAzureOpenAISettings(settings AzureOpenAISettings) error {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.SetSetting(SettingAzureOpenAI, string(encoded))
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

func (s *Store) CapabilityProviders() (CapabilityProviders, error) {
	encoded := s.Setting(SettingProviders)
	if encoded == "" {
		return CapabilityProviders{}, nil
	}
	var providers CapabilityProviders
	if err := json.Unmarshal([]byte(encoded), &providers); err != nil {
		return CapabilityProviders{}, fmt.Errorf("selección de proveedores inválida: %w", err)
	}
	return providers, nil
}

func (s *Store) SaveCapabilityProviders(providers CapabilityProviders) error {
	encoded, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	return s.SetSetting(SettingProviders, string(encoded))
}

// SaveSession records a session before its first message, so a session that
// never got an answer still shows up in the list.
func (s *Store) SaveSession(id, mode, language string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, started_at, mode, language) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, language = excluded.language`,
		id, time.Now().UTC().Format(time.RFC3339), mode, language)
	return err
}

func (s *Store) AppendMessage(sessionID, role, text string) error {
	_, err := s.db.Exec(
		"INSERT INTO messages (session_id, role, text, created_at) VALUES (?, ?, ?, ?)",
		sessionID, role, text, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) SaveVisualContext(sessionID, text string) error {
	_, err := s.db.Exec("UPDATE sessions SET visual_context = ? WHERE id = ?", text, sessionID)
	return err
}

// SessionNotesMaxChars is room for several jobs and projects described with
// role, stack and result. Keep in step with the dialog's maxlength.
const SessionNotesMaxChars = 4000

// SessionProfile is interview-specific context saved with its session.
type SessionProfile struct {
	Scenario  string `json:"scenario"`
	Company   string `json:"company"`
	Role      string `json:"role"`
	Objective string `json:"objective"`
	Notes     string `json:"notes"`
}

func NormalizeSessionProfile(profile SessionProfile) SessionProfile {
	profile.Scenario = strutil.Truncate(strings.TrimSpace(profile.Scenario), 120)
	if profile.Scenario == "" {
		profile.Scenario = "General"
	}
	profile.Company = strutil.Truncate(strings.TrimSpace(profile.Company), 160)
	profile.Role = strutil.Truncate(strings.TrimSpace(profile.Role), 160)
	profile.Objective = strutil.Truncate(strings.TrimSpace(profile.Objective), 400)
	// The dialog caps notes at sessionNotesMaxChars characters; this is in
	// bytes, and an accented letter takes two.
	profile.Notes = strutil.Truncate(strings.TrimSpace(profile.Notes), SessionNotesMaxChars*3)
	return profile
}

func (profile SessionProfile) PromptContext() string {
	profile = NormalizeSessionProfile(profile)
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

func (s *Store) SaveSessionProfile(sessionID string, profile SessionProfile) error {
	encoded, err := json.Marshal(NormalizeSessionProfile(profile))
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

func (s *Store) SessionProfile(sessionID string) (SessionProfile, error) {
	var encoded string
	if err := s.db.QueryRow("SELECT session_profile FROM sessions WHERE id = ?", sessionID).Scan(&encoded); err != nil {
		return SessionProfile{}, err
	}
	var profile SessionProfile
	if err := json.Unmarshal([]byte(encoded), &profile); err != nil {
		return SessionProfile{}, fmt.Errorf("sesión %s tiene contexto inválido: %w", sessionID, err)
	}
	return NormalizeSessionProfile(profile), nil
}

// LoadMessages returns the last limit messages in the order they were spoken.
func (s *Store) LoadMessages(sessionID string, limit int) ([]Message, error) {
	rows, err := s.db.Query(
		"SELECT role, text FROM (SELECT id, role, text FROM messages WHERE session_id = ? ORDER BY id DESC LIMIT ?) ORDER BY id ASC",
		sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.Role, &message.Text); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) VisualContext(sessionID string) string {
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

func (s *Store) SessionMessages(sessionID string) ([]SessionViewMessage, error) {
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

func (s *Store) RecentSessions(limit int) ([]SessionSummary, error) {
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
		item.Preview = strutil.Truncate(item.Preview, 120)
		sessions = append(sessions, item)
	}
	return sessions, rows.Err()
}

func (s *Store) SaveTitle(sessionID, title string) error {
	_, err := s.db.Exec("UPDATE sessions SET title = ? WHERE id = ?", title, sessionID)
	return err
}

// NeedsTitle reports whether a session is still unnamed. Titling costs a model
// call, so it happens once and only once per session.
func (s *Store) NeedsTitle(sessionID string) bool {
	var title string
	if err := s.db.QueryRow("SELECT title FROM sessions WHERE id = ?", sessionID).Scan(&title); err != nil {
		return false
	}
	return title == ""
}

func (s *Store) DeleteSession(id string) error {
	result, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("sesión %s no existe", id)
	}
	return nil
}

// Message is one turn of a session's conversation as stored.
type Message struct {
	Role string
	Text string
}
