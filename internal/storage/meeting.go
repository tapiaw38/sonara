package storage

import (
	"fmt"
	"strings"
)

// Tracks of a meeting recording. The microphone is the user by physical origin;
// the meeting output carries everyone else.
const (
	TrackMicrophone = "microphone"
	TrackOutput     = "meeting_output"
)

const (
	SegmentOK     = "ok"
	SegmentFailed = "failed"
	SegmentGap    = "gap"
	// SegmentEcho (phase 3): a meeting_output segment that turned out to be
	// the user's own voice played back through someone else's speakers. Text
	// is kept, never deleted; it just stops being shown as a participant.
	SegmentEcho = "echo"
)

const MeetingMode = "meeting"

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
func ParticipantLabel(track string, index int) string {
	if track == TrackMicrophone {
		if index < 26 {
			return "Participante " + string(rune('A'+index))
		}
		return fmt.Sprintf("Participante A%d", index)
	}
	return fmt.Sprintf("Participante %d", index+1)
}

func SpeakerDisplayKey(track, speakerKey string) string {
	if speakerKey == "" {
		return track
	}
	return track + ":" + speakerKey
}

func (s *Store) InsertSegment(sessionID string, segment TranscriptSegment) (int64, error) {
	result, err := s.db.Exec(
		"INSERT INTO transcript_segments (session_id, track, start_ms, end_ms, text, status, speaker_key) VALUES (?, ?, ?, ?, ?, ?, ?)",
		sessionID, segment.Track, segment.StartMS, segment.EndMS, segment.Text, segment.Status, segment.SpeakerKey)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) UpdateSegment(id int64, text, status string) error {
	_, err := s.db.Exec("UPDATE transcript_segments SET text = ?, status = ? WHERE id = ?", text, status, id)
	return err
}

// SetSegmentStatus changes only the status, keeping the stored text exactly
// as it was transcribed — used to mark a segment as an echo (phase 3)
// without ever discarding what was actually said.
func (s *Store) SetSegmentStatus(id int64, status string) error {
	_, err := s.db.Exec("UPDATE transcript_segments SET status = ? WHERE id = ?", status, id)
	return err
}

// SetSegmentSpeaker records which diarized participant a stored segment
// belongs to. Called once per segment, after capture stops and the whole
// meeting_output track has been diarized together.
func (s *Store) SetSegmentSpeaker(id int64, speakerKey string) error {
	_, err := s.db.Exec("UPDATE transcript_segments SET speaker_key = ? WHERE id = ?", speakerKey, id)
	return err
}

// TranscriptSegments returns a meeting in time order; the id breaks ties so
// two tracks that start together keep the order they were stored in.
func (s *Store) TranscriptSegments(sessionID string) ([]TranscriptSegment, error) {
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

// SaveSpeakerName renames one track (speakerKey "") or one diarized
// participant within it (speakerKey "spk_N").
func (s *Store) SaveSpeakerName(sessionID, track, speakerKey, name string) error {
	name = strings.TrimSpace(name)
	if track != TrackMicrophone && track != TrackOutput {
		return fmt.Errorf("pista desconocida %q", track)
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

// SpeakerNames returns the display name for every track and, once
// diarization has run, every participant found within meeting_output —
// keyed by speakerDisplayKey. Diarized participants are numbered by when they
// first spoke before any manual rename is applied on top.
func (s *Store) SpeakerNames(sessionID string) (map[string]string, error) {
	names := map[string]string{TrackMicrophone: "Yo", TrackOutput: "Participante"}
	// The microphone's people are lettered (A, B…) and the call's numbered
	// (1, 2…), so the two tracks never share a label.
	for _, track := range []string{TrackMicrophone, TrackOutput} {
		rows, err := s.db.Query(`
			SELECT speaker_key FROM transcript_segments
			WHERE session_id = ? AND track = ? AND speaker_key != '' AND status = ?
			GROUP BY speaker_key ORDER BY MIN(start_ms)`,
			sessionID, track, SegmentOK)
		if err != nil {
			return nil, err
		}
		index := 0
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, err
			}
			names[SpeakerDisplayKey(track, key)] = ParticipantLabel(track, index)
			index++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

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
		names[SpeakerDisplayKey(track, key)] = name
	}
	return names, overrides.Err()
}

// SessionInfo is the header data of one stored session.
func (s *Store) SessionInfo(sessionID string) (startedAt, mode, language string, err error) {
	err = s.db.QueryRow("SELECT started_at, mode, language FROM sessions WHERE id = ?", sessionID).
		Scan(&startedAt, &mode, &language)
	if err != nil {
		return "", "", "", fmt.Errorf("sesión %s no existe", sessionID)
	}
	return startedAt, mode, language, nil
}
