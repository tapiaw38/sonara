package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// openTestStore builds a store in a temp directory instead of the real config
// directory, so tests never touch the user's sessions or key.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sonara.db")
	database, err := OpenAt(path)
	if err != nil {
		t.Fatalf("openStoreAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestStoreKeepsTheAPIKeyAndClearsIt(t *testing.T) {
	database := openTestStore(t)

	if got := database.Setting(SettingAPIKey); got != "" {
		t.Errorf("a fresh store returned %q, want no key", got)
	}
	if err := database.SetSetting(SettingAPIKey, "sk-test-value"); err != nil {
		t.Fatalf("setSetting: %v", err)
	}
	if got := database.Setting(SettingAPIKey); got != "sk-test-value" {
		t.Errorf("stored key = %q", got)
	}
	// Overwriting must replace, not accumulate: the key is a primary-key upsert.
	if err := database.SetSetting(SettingAPIKey, "sk-second"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got := database.Setting(SettingAPIKey); got != "sk-second" {
		t.Errorf("overwritten key = %q, want sk-second", got)
	}
	// Clearing has to remove the secret, not store an empty string.
	if err := database.SetSetting(SettingAPIKey, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := database.Setting(SettingAPIKey); got != "" {
		t.Errorf("cleared key = %q, want empty", got)
	}
}

// The key lives in this file in plain text, so the mode is the only thing
// standing between it and every other user on the machine.
func TestStoreFileIsNotReadableByOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sonara.db")
	// Simulate a file restored from a backup with loose permissions.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	database, err := OpenAt(path)
	if err != nil {
		t.Fatalf("openStoreAt: %v", err)
	}
	defer database.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("store mode = %04o, want 0600", mode)
	}
}

func TestStoreRoundTripsASession(t *testing.T) {
	database := openTestStore(t)

	if err := database.SaveSession("s1", "interview", "es"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	for _, message := range []struct{ role, text string }{
		{"user", "primera pregunta"},
		{"assistant", "primera respuesta"},
		{"user", "segunda pregunta"},
	} {
		if err := database.AppendMessage("s1", message.role, message.text); err != nil {
			t.Fatalf("appendMessage: %v", err)
		}
	}
	if err := database.SaveVisualContext("s1", "captura analizada"); err != nil {
		t.Fatalf("saveVisualContext: %v", err)
	}

	messages, err := database.LoadMessages("s1", 10)
	if err != nil {
		t.Fatalf("loadMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("loaded %d messages, want 3", len(messages))
	}
	// Order matters: the prompt reads the history as a conversation.
	if messages[0].Text != "primera pregunta" || messages[2].Text != "segunda pregunta" {
		t.Errorf("messages came back out of order: %+v", messages)
	}
	if got := database.VisualContext("s1"); got != "captura analizada" {
		t.Errorf("visual context = %q", got)
	}

	// The limit has to keep the newest turns, which is what the prompt needs.
	recent, err := database.LoadMessages("s1", 2)
	if err != nil {
		t.Fatalf("loadMessages limited: %v", err)
	}
	if len(recent) != 2 || recent[0].Text != "primera respuesta" || recent[1].Text != "segunda pregunta" {
		t.Errorf("limited load = %+v, want the last two in order", recent)
	}
}

func TestStoreListsAndDeletesSessions(t *testing.T) {
	database := openTestStore(t)

	for _, id := range []string{"s1", "s2"} {
		if err := database.SaveSession(id, "interview", "es"); err != nil {
			t.Fatalf("saveSession %s: %v", id, err)
		}
	}
	if err := database.AppendMessage("s2", "user", "hola"); err != nil {
		t.Fatalf("appendMessage: %v", err)
	}

	sessions, err := database.RecentSessions(10)
	if err != nil {
		t.Fatalf("recentSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("listed %d sessions, want 2", len(sessions))
	}
	var withMessage SessionSummary
	for _, item := range sessions {
		if item.ID == "s2" {
			withMessage = item
		}
	}
	if withMessage.Messages != 1 || withMessage.Preview != "hola" {
		t.Errorf("s2 summary = %+v, want 1 message previewing hola", withMessage)
	}

	if err := database.DeleteSession("s2"); err != nil {
		t.Fatalf("deleteSession: %v", err)
	}
	// The cascade has to take the messages too, or they leak forever.
	messages, err := database.LoadMessages("s2", 10)
	if err != nil {
		t.Fatalf("loadMessages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("deleting the session left %d messages", len(messages))
	}
	if err := database.DeleteSession("missing"); err == nil {
		t.Error("deleting an unknown session must report an error")
	}
}

// A full notes field, accents included, survives the profile's own limit.
func TestSessionNotesKeepFullLengthInContext(t *testing.T) {
	notes := strings.Repeat("á", SessionNotesMaxChars-4) + " fin"
	if got := NormalizeSessionProfile(SessionProfile{Notes: notes}).Notes; got != notes {
		t.Fatalf("notes truncated to %d characters", utf8.RuneCountInString(got))
	}
}
