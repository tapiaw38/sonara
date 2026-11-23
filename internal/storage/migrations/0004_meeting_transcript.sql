-- Meeting transcripts: one session per recorded meeting (sessions.mode = 'meeting'),
-- one row per transcribed block of one audio track. Only text is stored; the
-- audio is dropped as soon as a block has been transcribed.
CREATE TABLE IF NOT EXISTS transcript_segments (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	track      TEXT NOT NULL CHECK (track IN ('microphone', 'meeting_output')),
	start_ms   INTEGER NOT NULL,
	end_ms     INTEGER NOT NULL,
	text       TEXT NOT NULL DEFAULT '',
	-- ok: transcribed. failed: the request failed, a gap the export must show.
	-- gap: the track was not being captured (device gone, queue full).
	status     TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'failed', 'gap'))
);

CREATE INDEX IF NOT EXISTS transcript_by_time ON transcript_segments(session_id, start_ms);
CREATE INDEX IF NOT EXISTS transcript_by_track ON transcript_segments(session_id, track, start_ms);

-- Display names a user gave to a track in one meeting ("Yo" and "Participante"
-- are the defaults when no row exists).
CREATE TABLE IF NOT EXISTS transcript_speakers (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	track      TEXT NOT NULL,
	name       TEXT NOT NULL,
	PRIMARY KEY (session_id, track)
);
