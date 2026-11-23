package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"sonara/internal/fsutil"
	"sonara/internal/meeting"
)

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
	text := meeting.FormatTranscript(started.Local(), view.Language, view.Segments, view.Speakers)
	return fsutil.WriteAtomic(path, []byte(text+a.meetingSummary(text)))
}

// meetingSummary never fails the export: a missing summary is noted, the
// transcript is still written.
func (a *App) meetingSummary(transcript string) string {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	if backend == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 90*time.Second)
	defer cancel()
	output, err := backend.Respond(ctx, "Resumí esta reunión:\n\n"+transcript, "summary", "")
	if err != nil {
		logf("meeting summary failed: %v", err)
		return "\n\n===== RESUMEN =====\n(no se pudo generar el resumen)\n"
	}
	return "\n\n===== RESUMEN =====\n" + strings.TrimSpace(output.Text) + "\n"
}

func nativeSaveDialog(ctx context.Context, filename string) (string, error) {
	return wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
		Title:            "Exportar transcripción",
		DefaultDirectory: meeting.DefaultExportDir(),
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
	path, err := dialog(ctx, meeting.ExportFilename(view.StartedAt))
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
