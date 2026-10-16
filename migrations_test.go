package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLoadMigrationsAreOrderedAndUnique(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations were embedded; the //go:embed pattern is wrong")
	}
	for i, item := range migrations {
		if item.sql == "" {
			t.Errorf("migration %s is empty", item.name)
		}
		if i > 0 && item.version <= migrations[i-1].version {
			t.Errorf("migrations out of order: %s then %s", migrations[i-1].name, item.name)
		}
	}
}

func TestMigrateAppliesEverythingToAFreshDatabase(t *testing.T) {
	database := openTestStore(t) // openStoreAt runs migrate

	var version int
	if err := database.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	want := migrations[len(migrations)-1].version
	if version != want {
		t.Errorf("fresh database is at version %d, want %d", version, want)
	}
	// The title column only exists if 0002 actually ran.
	if err := database.saveTitle("nope", "x"); err != nil {
		t.Errorf("saveTitle on a migrated database failed: %v", err)
	}
}

// The real upgrade path: a database created before migrations existed, holding
// rows that must survive, with no title column and user_version still at 0.
func TestMigrateUpgradesAPreMigrationDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sonara.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = legacy.Exec(`
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at TEXT NOT NULL,
			mode TEXT NOT NULL DEFAULT 'interview', language TEXT NOT NULL DEFAULT 'es',
			visual_context TEXT NOT NULL DEFAULT '');
		CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			role TEXT NOT NULL, text TEXT NOT NULL, created_at TEXT NOT NULL);
		INSERT INTO sessions (id, started_at) VALUES ('old', '2026-10-16T00:00:00Z');
		INSERT INTO messages (session_id, role, text, created_at)
			VALUES ('old', 'user', 'pregunta vieja', '2026-10-16T00:00:00Z');
		INSERT INTO settings (key, value) VALUES ('openai_api_key', 'sk-kept');`)
	if err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	legacy.Close()

	database, err := openStoreAt(path)
	if err != nil {
		t.Fatalf("openStoreAt on a legacy database: %v", err)
	}
	defer database.Close()

	// Existing rows must be untouched: this is the user's interview history.
	if got := database.setting(settingAPIKey); got != "sk-kept" {
		t.Errorf("the stored key did not survive migration: %q", got)
	}
	messages, err := database.loadMessages("old", 10)
	if err != nil || len(messages) != 1 || messages[0].text != "pregunta vieja" {
		t.Errorf("messages did not survive migration: %+v, err %v", messages, err)
	}
	// And the new column has to be there, defaulted rather than null.
	sessions, err := database.recentSessions(10)
	if err != nil {
		t.Fatalf("recentSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Title != "" {
		t.Errorf("sessions = %+v, want one row with an empty title", sessions)
	}
	if !database.needsTitle("old") {
		t.Error("a migrated session should report that it still needs a title")
	}
}

// Running twice must be a no-op, since this happens on every single startup.
func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sonara.db")
	first, err := openStoreAt(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.saveSession("s1", "interview", "es"); err != nil {
		t.Fatalf("saveSession: %v", err)
	}
	first.Close()

	second, err := openStoreAt(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	sessions, err := second.recentSessions(10)
	if err != nil || len(sessions) != 1 {
		t.Errorf("reopening lost data: %+v, err %v", sessions, err)
	}
}
