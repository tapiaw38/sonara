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
}

func (s *store) insertSegment(sessionID string, segment TranscriptSegment) (int64, error) {
	result, err := s.db.Exec(
		"INSERT INTO transcript_segments (session_id, track, start_ms, end_ms, text, status) VALUES (?, ?, ?, ?, ?, ?)",
		sessionID, segment.Track, segment.StartMS, segment.EndMS, segment.Text, segment.Status)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *store) updateSegment(id int64, text, status string) error {
	_, err := s.db.Exec("UPDATE transcript_segments SET text = ?, status = ? WHERE id = ?", text, status, id)
	return err
}

// transcriptSegments returns a meeting in time order; the id breaks ties so
// two tracks that start together keep the order they were stored in.
func (s *store) transcriptSegments(sessionID string) ([]TranscriptSegment, error) {
	rows, err := s.db.Query(
		"SELECT id, track, start_ms, end_ms, text, status FROM transcript_segments WHERE session_id = ? ORDER BY start_ms, id",
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	segments := []TranscriptSegment{}
	for rows.Next() {
		var item TranscriptSegment
		if err := rows.Scan(&item.ID, &item.Track, &item.StartMS, &item.EndMS, &item.Text, &item.Status); err != nil {
			return nil, err
		}
		segments = append(segments, item)
	}
	return segments, rows.Err()
}

func (s *store) saveSpeakerName(sessionID, track, name string) error {
	name = strings.TrimSpace(name)
	if track != trackMicrophone && track != trackOutput {
		return fmt.Errorf("pista desconocida %q", track)
	}
	if name == "" {
		_, err := s.db.Exec("DELETE FROM transcript_speakers WHERE session_id = ? AND track = ?", sessionID, track)
		return err
	}
	if len([]rune(name)) > 60 || strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("nombre inválido")
	}
	_, err := s.db.Exec(`
		INSERT INTO transcript_speakers (session_id, track, name) VALUES (?, ?, ?)
		ON CONFLICT(session_id, track) DO UPDATE SET name = excluded.name`,
		sessionID, track, name)
	return err
}

// speakerNames returns the display name of each track, defaults filled in.
func (s *store) speakerNames(sessionID string) (map[string]string, error) {
	names := map[string]string{trackMicrophone: "Yo", trackOutput: "Participante"}
	rows, err := s.db.Query("SELECT track, name FROM transcript_speakers WHERE session_id = ?", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var track, name string
		if err := rows.Scan(&track, &name); err != nil {
			return nil, err
		}
		names[track] = name
	}
	return names, rows.Err()
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
