-- Baseline schema. Written with IF NOT EXISTS so a database created before
-- migrations existed is adopted as version 1 without touching its rows.
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id             TEXT PRIMARY KEY,
	started_at     TEXT NOT NULL,
	mode           TEXT NOT NULL DEFAULT 'interview',
	language       TEXT NOT NULL DEFAULT 'es',
	visual_context TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	role       TEXT NOT NULL,
	text       TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS messages_by_session ON messages(session_id, id);
