package meeting

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sonara/internal/storage"
)

func languageName(code string) string {
	switch code {
	case "es":
		return "español"
	case "en":
		return "inglés"
	}
	return "automático"
}

func clock(ms int64) string {
	seconds := max(ms, 0) / 1000
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// FormatTranscript renders a stored meeting as UTF-8 text. It works from
// the stored rows, never from what the UI happens to show, so a reopened
// meeting exports exactly like a live one. Failed and uncaptured stretches are
// written out: a missing minute must be visible, not silently absent.
func FormatTranscript(startedAt time.Time, language string, segments []storage.TranscriptSegment, speakers map[string]string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Reunión: %s\nIdioma: %s\n\n", startedAt.Format("2006-01-02 15:04"), languageName(language))
	for _, segment := range segments {
		switch segment.Status {
		case storage.SegmentOK:
			name := trackLabel(segment.Track, segment.SpeakerKey, speakers)
			if name == "" {
				name = "Hablante desconocido"
			}
			fmt.Fprintf(&out, "[%s] %s: %s\n", clock(segment.StartMS), name, segment.Text)
		case storage.SegmentFailed:
			fmt.Fprintf(&out, "[%s–%s] (sin transcribir: falló la transcripción, %s)\n",
				clock(segment.StartMS), clock(segment.EndMS), trackLabel(segment.Track, segment.SpeakerKey, speakers))
		case storage.SegmentEcho:
			// Not missing audio, not a failure: deliberately excluded because
			// it is the user's own voice (already in the "Yo" lines) echoed
			// back through someone else's speakers — kept here as a note
			// rather than silently vanishing, same as every other status.
			fmt.Fprintf(&out, "[%s] (eco de tu propia voz, omitido como duplicado)\n", clock(segment.StartMS))
		default:
			fmt.Fprintf(&out, "[%s–%s] (sin transcribir: audio no capturado, %s)\n",
				clock(segment.StartMS), clock(segment.EndMS), trackLabel(segment.Track, segment.SpeakerKey, speakers))
		}
	}
	return out.String()
}

// trackLabel resolves a segment's display name the same way the UI does: a
// diarized participant's own name or number, falling back to the track's
// generic one, falling back to the raw track string.
func trackLabel(track, speakerKey string, speakers map[string]string) string {
	if name := speakers[storage.SpeakerDisplayKey(track, speakerKey)]; name != "" {
		return name
	}
	if name := speakers[track]; name != "" {
		return name
	}
	return track
}

// ExportFilename is the suggested file name for a meeting started at startedAt.
func ExportFilename(startedAt string) string {
	started, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return "reunion.txt"
	}
	return "reunion-" + started.Local().Format("2006-01-02-1504") + ".txt"
}

// DefaultExportDir is the folder the save dialog opens in: Documents when it
// exists, the home directory otherwise.
func DefaultExportDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"Documentos", "Documents"} {
		if info, err := os.Stat(filepath.Join(home, name)); err == nil && info.IsDir() {
			return filepath.Join(home, name)
		}
	}
	return home
}
