package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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

// formatMeetingTranscript renders a stored meeting as UTF-8 text. It works from
// the stored rows, never from what the UI happens to show, so a reopened
// meeting exports exactly like a live one. Failed and uncaptured stretches are
// written out: a missing minute must be visible, not silently absent.
func formatMeetingTranscript(startedAt time.Time, language string, segments []TranscriptSegment, speakers map[string]string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Reunión: %s\nIdioma: %s\n\n", startedAt.Format("2006-01-02 15:04"), languageName(language))
	for _, segment := range segments {
		switch segment.Status {
		case segmentOK:
			name := trackLabel(segment.Track, segment.SpeakerKey, speakers)
			if name == "" {
				name = "Hablante desconocido"
			}
			fmt.Fprintf(&out, "[%s] %s: %s\n", clock(segment.StartMS), name, segment.Text)
		case segmentFailed:
			fmt.Fprintf(&out, "[%s–%s] (sin transcribir: falló la transcripción, %s)\n",
				clock(segment.StartMS), clock(segment.EndMS), trackLabel(segment.Track, segment.SpeakerKey, speakers))
		case segmentEcho:
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
	if name := speakers[speakerDisplayKey(track, speakerKey)]; name != "" {
		return name
	}
	if name := speakers[track]; name != "" {
		return name
	}
	return track
}

// writeFileAtomic writes beside the destination and renames, so a failed or
// interrupted export never leaves a half-written .txt where a good one was.
func writeFileAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".sonara-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	// Transcripts are private: same mode as the database.
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// exportMeetingTo writes a stored meeting to path.
func (a *App) exportMeetingTo(sessionID, path string) error {
	view, err := a.MeetingTranscriptSegments(sessionID)
	if err != nil {
		return err
	}
	started, err := time.Parse(time.RFC3339, view.StartedAt)
	if err != nil {
		started = time.Now()
	}
	text := formatMeetingTranscript(started.Local(), view.Language, view.Segments, view.Speakers)
	return writeFileAtomic(path, []byte(text+a.meetingSummary(text)))
}

// meetingSummary never fails the export: a missing summary is noted, the
// transcript is still written.
func (a *App) meetingSummary(transcript string) string {
	if a.backend == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 90*time.Second)
	defer cancel()
	output, err := a.backend.Respond(ctx, "Resumí esta reunión:\n\n"+transcript, "summary", "")
	if err != nil {
		logf("meeting summary failed: %v", err)
		return "\n\n===== RESUMEN =====\n(no se pudo generar el resumen)\n"
	}
	return "\n\n===== RESUMEN =====\n" + strings.TrimSpace(output.Text) + "\n"
}

func exportFilename(startedAt string) string {
	started, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return "reunion.txt"
	}
	return "reunion-" + started.Local().Format("2006-01-02-1504") + ".txt"
}

// defaultExportDir is the folder the save dialog opens in: Documents when it
// exists, the home directory otherwise.
func defaultExportDir() string {
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

func nativeSaveDialog(ctx context.Context, filename string) (string, error) {
	return wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
		Title:            "Exportar transcripción",
		DefaultDirectory: defaultExportDir(),
		DefaultFilename:  filename,
		Filters:          []wruntime.FileFilter{{DisplayName: "Texto (*.txt)", Pattern: "*.txt"}},
	})
}

// ExportMeetingTranscript asks where to save and writes the .txt. It returns the
// path, or "" when the user cancels the dialog.
func (a *App) ExportMeetingTranscript(sessionID string) (string, error) {
	view, err := a.MeetingTranscriptSegments(sessionID)
	if err != nil {
		return "", err
	}
	a.mu.RLock()
	ctx, dialog := a.ctx, a.saveDialog
	a.mu.RUnlock()
	if dialog == nil {
		if ctx == nil {
			return "", fmt.Errorf("la ventana todavía no está lista")
		}
		dialog = nativeSaveDialog
	}
	path, err := dialog(ctx, exportFilename(view.StartedAt))
	if err != nil || path == "" {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(path), ".txt") {
		path += ".txt"
	}
	if err := a.exportMeetingTo(sessionID, path); err != nil {
		return "", fmt.Errorf("no se pudo escribir %s: %w", path, err)
	}
	logf("meeting %s exported", sessionID)
	return path, nil
}
