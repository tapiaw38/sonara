package main

import (
	"fmt"
	"strings"
)

// Tracks of a meeting recording. The microphone is the user by physical origin;
// the meeting output carries everyone else.
const (
	trackMicrophone = "microphone"
	trackOutput     = "meeting_output"
)

const (
	segmentOK     = "ok"
	segmentFailed = "failed"
	segmentGap    = "gap"
)

const meetingMode = "meeting"

// TranscriptSegment is one transcribed block of one track, positioned on the
// recording's shared clock (milliseconds since the recording started).
type TranscriptSegment struct {
	ID      int64  `json:"id"`
	Track   string `json:"track"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
	Text    string `json:"text"`
	Status  string `json:"status"`
	// SpeakerKey identifies one diarized participant within the meeting_output
	// track ("" until phase-2 diarization runs, or when it is off). It is
	// always "" for the microphone track: that one is "Yo" by physical origin,
	// never by voice analysis.
	SpeakerKey string `json:"speakerKey"`
}

// speakerDisplayKey is the key speakerNames uses for one segment: the track
// alone when it has no diarized speaker, or track+speakerKey when it does.
// Export and the UI both resolve a name through this same key so they never
// disagree on who said what.
func speakerDisplayKey(track, speakerKey string) string {
	if speakerKey == "" {
		return track
	}
	return track + ":" + speakerKey
}

func (s *store) insertSegment(sessionID string, segment TranscriptSegment) (int64, error) {
	result, err := s.db.Exec(
		"INSERT INTO transcript_segments (session_id, track, start_ms, end_ms, text, status, speaker_key) VALUES (?, ?, ?, ?, ?, ?, ?)",
		sessionID, segment.Track, segment.StartMS, segment.EndMS, segment.Text, segment.Status, segment.SpeakerKey)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *store) updateSegment(id int64, text, status string) error {
	_, err := s.db.Exec("UPDATE transcript_segments SET text = ?, status = ? WHERE id = ?", text, status, id)
	return err
}

// setSegmentSpeaker records which diarized participant a stored segment
// belongs to. Called once per segment, after capture stops and the whole
// meeting_output track has been diarized together.
func (s *store) setSegmentSpeaker(id int64, speakerKey string) error {
	_, err := s.db.Exec("UPDATE transcript_segments SET speaker_key = ? WHERE id = ?", speakerKey, id)
	return err
}

// transcriptSegments returns a meeting in time order; the id breaks ties so
// two tracks that start together keep the order they were stored in.
func (s *store) transcriptSegments(sessionID string) ([]TranscriptSegment, error) {
	rows, err := s.db.Query(
		"SELECT id, track, start_ms, end_ms, text, status, speaker_key FROM transcript_segments WHERE session_id = ? ORDER BY start_ms, id",
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	segments := []TranscriptSegment{}
	for rows.Next() {
		var item TranscriptSegment
		if err := rows.Scan(&item.ID, &item.Track, &item.StartMS, &item.EndMS, &item.Text, &item.Status, &item.SpeakerKey); err != nil {
			return nil, err
		}
		segments = append(segments, item)
	}
	return segments, rows.Err()
}

// saveSpeakerName renames one track (speakerKey "") or one diarized
// participant within the meeting_output track (speakerKey "spk_N").
func (s *store) saveSpeakerName(sessionID, track, speakerKey, name string) error {
	name = strings.TrimSpace(name)
	if track != trackMicrophone && track != trackOutput {
		return fmt.Errorf("pista desconocida %q", track)
	}
	if speakerKey != "" && track != trackOutput {
		return fmt.Errorf("sólo la pista de la reunión tiene participantes diarizados")
	}
	if name == "" {
		_, err := s.db.Exec("DELETE FROM transcript_speakers WHERE session_id = ? AND track = ? AND speaker_key = ?", sessionID, track, speakerKey)
		return err
	}
	if len([]rune(name)) > 60 || strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("nombre inválido")
	}
	_, err := s.db.Exec(`
		INSERT INTO transcript_speakers (session_id, track, speaker_key, name) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, track, speaker_key) DO UPDATE SET name = excluded.name`,
		sessionID, track, speakerKey, name)
	return err
}

// speakerNames returns the display name for every track and, once
// diarization has run, every participant found within meeting_output —
// keyed by speakerDisplayKey. Diarized participants are numbered by when they
// first spoke before any manual rename is applied on top.
func (s *store) speakerNames(sessionID string) (map[string]string, error) {
	names := map[string]string{trackMicrophone: "Yo", trackOutput: "Participante"}
	rows, err := s.db.Query(`
		SELECT speaker_key FROM transcript_segments
		WHERE session_id = ? AND track = ? AND speaker_key != '' AND status = ?
		GROUP BY speaker_key ORDER BY MIN(start_ms)`,
		sessionID, trackOutput, segmentOK)
	if err != nil {
		return nil, err
	}
	number := 1
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		names[speakerDisplayKey(trackOutput, key)] = fmt.Sprintf("Participante %d", number)
		number++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	overrides, err := s.db.Query("SELECT track, speaker_key, name FROM transcript_speakers WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, err
	}
	defer overrides.Close()
	for overrides.Next() {
		var track, key, name string
		if err := overrides.Scan(&track, &key, &name); err != nil {
			return nil, err
		}
		names[speakerDisplayKey(track, key)] = name
	}
	return names, overrides.Err()
}

// sessionInfo is the header data of one stored session.
func (s *store) sessionInfo(sessionID string) (startedAt, mode, language string, err error) {
	err = s.db.QueryRow("SELECT started_at, mode, language FROM sessions WHERE id = ?", sessionID).
		Scan(&startedAt, &mode, &language)
	if err != nil {
		return "", "", "", fmt.Errorf("sesión %s no existe", sessionID)
	}
	return startedAt, mode, language, nil
}
