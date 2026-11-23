-- Phase 2: speaker diarization of the meeting_output track. A block keeps its
-- generic track label until diarization (run once, after capture stops) fills
-- in which of several participants it belongs to.
ALTER TABLE transcript_segments ADD COLUMN speaker_key TEXT NOT NULL DEFAULT '';

-- transcript_speakers gains a third key component so each diarized participant
-- can be renamed independently of the others on the same track. Nothing else
-- in the app declares a foreign key against this table, so it can be rebuilt
-- in place: existing rows become speaker_key = '' (the track-level name they
-- already were, e.g. the generic "Participante" override).
ALTER TABLE transcript_speakers RENAME TO transcript_speakers_old;

CREATE TABLE transcript_speakers (
	session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	track       TEXT NOT NULL,
	speaker_key TEXT NOT NULL DEFAULT '',
	name        TEXT NOT NULL,
	PRIMARY KEY (session_id, track, speaker_key)
);

INSERT INTO transcript_speakers (session_id, track, speaker_key, name)
	SELECT session_id, track, '', name FROM transcript_speakers_old;

DROP TABLE transcript_speakers_old;
