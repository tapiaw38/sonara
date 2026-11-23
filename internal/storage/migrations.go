package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"sonara/internal/logx"
)

// Schema changes live in migrations/*.sql, numbered, and are embedded in the
// binary so the installed app carries them. They run at every startup, not at
// build time: a build cannot reach the database already sitting in a user's
// config directory, and that is the one that needs upgrading.
//
// Versioning uses SQLite's own PRAGMA user_version rather than a migrations
// table or a migration library. It is a 32-bit slot built into the file, which
// is all a single-process desktop app needs; goose and golang-migrate buy
// up/down and status that nothing here would call.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded files in version order and rejects anything
// malformed or duplicated: a migration set that cannot be trusted to be ordered
// is worse than none.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			return nil, fmt.Errorf("migration %q must be named <version>_<name>.sql", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration %q has no usable version prefix", entry.Name())
		}
		body, err := migrationFiles.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, migration{version: version, name: entry.Name(), sql: string(body)})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i := range migrations {
		if i > 0 && migrations[i].version == migrations[i-1].version {
			return nil, fmt.Errorf("migrations %q and %q share version %d",
				migrations[i-1].name, migrations[i].name, migrations[i].version)
		}
	}
	return migrations, nil
}

// migrate applies whatever has not run yet. Each migration and its version bump
// share one transaction, so a failure leaves the database on the previous
// version and the same migration is retried on the next start.
func migrate(db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	for _, item := range migrations {
		if item.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(item.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", item.name, err)
		}
		// PRAGMA takes no placeholders; the value is an int parsed from the
		// filename, never user input.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", item.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: recording the version: %w", item.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", item.name, err)
		}
		logx.Logf("migration applied: %s", item.name)
		current = item.version
	}
	return nil
}
