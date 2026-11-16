-- Phase 3: a meeting_output segment that turns out to be an echo of the
-- user's own voice (heard through someone else's speakers) gets this status
-- instead of 'ok', so it stops showing up as a fake "Participante" line
-- without ever deleting its text. SQLite can't ALTER a CHECK constraint, so
-- the table is rebuilt; nothing else references transcript_segments.id, so
-- ids can be copied over as-is. AUTOINCREMENT is dropped (this app never
-- deletes one segment without the rest, so id reuse was never a concern) to
-- sidestep having to also repair sqlite_sequence by hand after the copy.
ALTER TABLE transcript_segments RENAME TO transcript_segments_old;

CREATE TABLE transcript_segments (
	id          INTEGER PRIMARY KEY,
	session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	track       TEXT NOT NULL CHECK (track IN ('microphone', 'meeting_output')),
	start_ms    INTEGER NOT NULL,
	end_ms      INTEGER NOT NULL,
	text        TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'failed', 'gap', 'echo')),
	speaker_key TEXT NOT NULL DEFAULT ''
);

INSERT INTO transcript_segments (id, session_id, track, start_ms, end_ms, text, status, speaker_key)
	SELECT id, session_id, track, start_ms, end_ms, text, status, speaker_key FROM transcript_segments_old;

DROP TABLE transcript_segments_old;

CREATE INDEX IF NOT EXISTS transcript_by_time ON transcript_segments(session_id, start_ms);
CREATE INDEX IF NOT EXISTS transcript_by_track ON transcript_segments(session_id, track, start_ms);
