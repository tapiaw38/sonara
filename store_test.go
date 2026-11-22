package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// openTestStore builds a store in a temp directory instead of the real config
// directory, so tests never touch the user's sessions or key.
func openTestStore(t *testing.T) *store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sonara.db")
	database, err := openStoreAt(path)
	if err != nil {
		t.Fatalf("openStoreAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestStoreKeepsTheAPIKeyAndClearsIt(t *testing.T) {
	database := openTestStore(t)

	if got := database.setting(settingAPIKey); got != "" {
		t.Errorf("a fresh store returned %q, want no key", got)
	}
	if err := database.setSetting(settingAPIKey, "sk-test-value"); err != nil {
		t.Fatalf("setSetting: %v", err)
	}
	if got := database.setting(settingAPIKey); got != "sk-test-value" {
		t.Errorf("stored key = %q", got)
	}
	// Overwriting must replace, not accumulate: the key is a primary-key upsert.
	if err := database.setSetting(settingAPIKey, "sk-second"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got := database.setting(settingAPIKey); got != "sk-second" {
		t.Errorf("overwritten key = %q, want sk-second", got)
	}
	// Clearing has to remove the secret, not store an empty string.
	if err := database.setSetting(settingAPIKey, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := database.setting(settingAPIKey); got != "" {
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
	database, err := openStoreAt(path)
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

	if err := database.saveSession("s1", "interview", "es"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	for _, message := range []struct{ role, text string }{
		{"user", "primera pregunta"},
		{"assistant", "primera respuesta"},
		{"user", "segunda pregunta"},
	} {
		if err := database.appendMessage("s1", message.role, message.text); err != nil {
			t.Fatalf("appendMessage: %v", err)
		}
	}
	if err := database.saveVisualContext("s1", "captura analizada"); err != nil {
		t.Fatalf("saveVisualContext: %v", err)
	}

	messages, err := database.loadMessages("s1", 10)
	if err != nil {
		t.Fatalf("loadMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("loaded %d messages, want 3", len(messages))
	}
	// Order matters: the prompt reads the history as a conversation.
	if messages[0].text != "primera pregunta" || messages[2].text != "segunda pregunta" {
		t.Errorf("messages came back out of order: %+v", messages)
	}
	if got := database.visualContext("s1"); got != "captura analizada" {
		t.Errorf("visual context = %q", got)
	}

	// The limit has to keep the newest turns, which is what the prompt needs.
	recent, err := database.loadMessages("s1", 2)
	if err != nil {
		t.Fatalf("loadMessages limited: %v", err)
	}
	if len(recent) != 2 || recent[0].text != "primera respuesta" || recent[1].text != "segunda pregunta" {
		t.Errorf("limited load = %+v, want the last two in order", recent)
	}
}

func TestStoreListsAndDeletesSessions(t *testing.T) {
	database := openTestStore(t)

	for _, id := range []string{"s1", "s2"} {
		if err := database.saveSession(id, "interview", "es"); err != nil {
			t.Fatalf("saveSession %s: %v", id, err)
		}
	}
	if err := database.appendMessage("s2", "user", "hola"); err != nil {
		t.Fatalf("appendMessage: %v", err)
	}

	sessions, err := database.recentSessions(10)
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

	if err := database.deleteSession("s2"); err != nil {
		t.Fatalf("deleteSession: %v", err)
	}
	// The cascade has to take the messages too, or they leak forever.
	messages, err := database.loadMessages("s2", 10)
	if err != nil {
		t.Fatalf("loadMessages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("deleting the session left %d messages", len(messages))
	}
	if err := database.deleteSession("missing"); err == nil {
		t.Error("deleting an unknown session must report an error")
	}
}

// A full notes field, accents included, reaches the prompt whole: neither the
// profile's own limit nor the shared context budget cuts it.
func TestSessionNotesKeepFullLengthInContext(t *testing.T) {
	notes := strings.Repeat("á", sessionNotesMaxChars-4) + " fin"
	if got := normalizeSessionProfile(SessionProfile{Notes: notes}).Notes; got != notes {
		t.Fatalf("notes truncated to %d characters", utf8.RuneCountInString(got))
	}
	a := &App{sessionProfile: SessionProfile{Notes: notes}}
	if !strings.Contains(a.buildSessionContextLocked(), notes) {
		t.Fatal("notes cut from the session context")
	}
}
