package diarize

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sonara/backend/usage"
	"strconv"
)

// Local, free speaker separation with sherpa-onnx (pyannote segmentation plus
// a speaker-embedding model), used whenever no Gemini key is set. The CLI and
// models live in ~/.local/share/sherpa-onnx; without them diarization stays
// off, exactly as before.

func sherpaDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "sherpa-onnx")
}

func sherpaBin() string {
	matches, _ := filepath.Glob(filepath.Join(sherpaDir(), "sherpa-onnx-*", "bin", "sherpa-onnx-offline-speaker-diarization"))
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

// localClusterThreshold decides when two voices count as different people:
// lower splits more. sherpa-onnx's own 0.5 split two people on this laptop's
// built-in mic into 6 and 0.9 still drifted to 7 over a few minutes, so auto
// uses 1.0; a fixed MeetingPeople count is what actually holds. Tune
// with SONARA_DIARIZE_THRESHOLD if one person keeps coming out as two (raise
// it) or two people as one (lower it).
func localClusterThreshold() string {
	if value := os.Getenv("SONARA_DIARIZE_THRESHOLD"); value != "" {
		return value
	}
	return "1.0"
}

var sherpaSpanLine = regexp.MustCompile(`(?m)^\s*([0-9.]+) -- ([0-9.]+) (speaker_\d+)\s*$`)

// Sherpa is the local engine. The zero value is ready to use.
type Sherpa struct{}

// Available reports whether the CLI is installed.
func (Sherpa) Available() bool { return sherpaBin() != "" }

func (Sherpa) Diarize(ctx context.Context, wav []byte, speakers int) (_ []Span, callErr error) {
	bin := sherpaBin()
	if bin == "" {
		return nil, fmt.Errorf("no hay motor de separación: falta la clave de Gemini o sherpa-onnx en %s", sherpaDir())
	}
	file, err := os.CreateTemp("", "sonara-diarize-*.wav")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(wav); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	dir := sherpaDir()
	clustering := "--clustering.cluster-threshold=" + localClusterThreshold()
	if speakers > 0 {
		clustering = "--clustering.num-clusters=" + strconv.Itoa(speakers)
	}

	meter := usage.Start(ctx, "local", "sherpa-onnx", usage.Diarization)
	meter.AudioSeconds = usage.WAVSeconds(wav)
	defer func() { meter.Finish(callErr == nil) }()
	output, err := exec.CommandContext(ctx, bin, clustering,
		"--segmentation.num-threads=4", "--embedding.num-threads=4",
		"--segmentation.pyannote-model="+filepath.Join(dir, "sherpa-onnx-pyannote-segmentation-3-0", "model.onnx"),
		"--embedding.model="+filepath.Join(dir, "3dspeaker_speech_eres2net_sv_en_voxceleb_16k.onnx"),
		file.Name()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sherpa-onnx falló: %w", err)
	}
	return parseSherpaSpans(string(output)), nil
}

func parseSherpaSpans(output string) []Span {
	var spans []Span
	for _, match := range sherpaSpanLine.FindAllStringSubmatch(output, -1) {
		start, err1 := strconv.ParseFloat(match[1], 64)
		end, err2 := strconv.ParseFloat(match[2], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		spans = append(spans, Span{StartMS: int64(start * 1000), EndMS: int64(end * 1000), Speaker: match[3]})
	}
	return spans
}
