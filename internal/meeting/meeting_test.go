package meeting

import (
	"encoding/binary"
	"testing"
	"time"

	"sonara/internal/audio"
	"sonara/internal/storage"
)

func tone(amplitude int16) []byte {
	frame := make([]byte, audio.FrameBytes)
	for i := 0; i+1 < len(frame); i += 2 {
		binary.LittleEndian.PutUint16(frame[i:], uint16(amplitude))
	}
	return frame
}

func TestBlockCutterCutsAtPauseAfterMinimum(t *testing.T) {
	cutter := NewBlockCutter(storage.TrackMicrophone, nil)
	cutter.ambient.Observe(0)
	var blocks []Block
	for i := 0; i < 50; i++ {
		// Speech for the first 40 frames, then a pause.
		amp := int16(8000)
		if i >= 40 {
			amp = 0
		}
		if block, ok := cutter.Add(tone(amp), int64(i)*FrameMS); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 cut at the pause", len(blocks))
	}
	block := blocks[0]
	if block.StartMS != 0 || block.EndMS != 44*FrameMS || !block.Voiced {
		t.Errorf("block = %d-%dms voiced=%v, want 0-4400ms voiced", block.StartMS, block.EndMS, block.Voiced)
	}
	if len(block.PCM) != 44*audio.FrameBytes {
		t.Errorf("pcm length %d does not match the span", len(block.PCM))
	}
}

func TestBlockCutterHardLimitAndNoLostFrames(t *testing.T) {
	cutter := NewBlockCutter(storage.TrackOutput, nil)
	cutter.ambient.Observe(0)
	total, cuts := 0, 0
	var lastEnd int64
	for i := 0; i < 700; i++ {
		if block, ok := cutter.Add(tone(8000), int64(i)*FrameMS); ok {
			cuts++
			total += len(block.PCM) / audio.FrameBytes
			if block.StartMS != lastEnd {
				t.Fatalf("block starts at %d, previous ended at %d: audio lost or repeated", block.StartMS, lastEnd)
			}
			lastEnd = block.EndMS
		}
	}
	if block, ok := cutter.Flush(); ok {
		total += len(block.PCM) / audio.FrameBytes
	}
	if cuts != 8 || total != 700 {
		t.Errorf("continuous speech: %d cuts, %d frames kept; want 8 cuts and all 700 frames", cuts, total)
	}
}

// A mic whose hiss sits above meetNoiseFloor must still find the pause after
// speech instead of treating everything as voice until the hard limit.
func TestBlockCutterCalibratesOnHissyMicrophone(t *testing.T) {
	cutter := NewBlockCutter(storage.TrackMicrophone, nil)
	var blocks []Block
	for i := 0; i < 80; i++ {
		amp := int16(600) // ~0.018 RMS hiss
		if i >= 20 && i < 60 {
			amp = 8000
		}
		if block, ok := cutter.Add(tone(amp), int64(i)*FrameMS); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) != 1 || blocks[0].EndMS != 61*FrameMS || !blocks[0].Voiced {
		t.Fatalf("got %d blocks; want one voiced block cut at the pause, ending at 6100ms", len(blocks))
	}
}

func TestBlockCutterMarksSilenceUnvoiced(t *testing.T) {
	cutter := NewBlockCutter(storage.TrackMicrophone, nil)
	for i := 0; i < 20; i++ {
		cutter.Add(tone(0), int64(i)*FrameMS)
	}
	block, ok := cutter.Flush()
	if !ok || block.Voiced {
		t.Fatalf("silent block voiced=%v ok=%v; silence must not be uploaded", block.Voiced, ok)
	}
}

func TestFormatTranscript(t *testing.T) {
	started := time.Date(2026, 11, 16, 10, 30, 0, 0, time.UTC)
	got := FormatTranscript(started, "es", []storage.TranscriptSegment{
		{Track: storage.TrackMicrophone, StartMS: 12000, Text: "Buenos días. ¿Empezamos?", Status: storage.SegmentOK},
		{Track: storage.TrackOutput, StartMS: 16000, Text: "Sí, comparto pantalla.", Status: storage.SegmentOK},
		{Track: storage.TrackOutput, StartMS: 3_725_000, EndMS: 3_745_000, Status: storage.SegmentFailed},
		{Track: storage.TrackMicrophone, StartMS: 3_800_000, EndMS: 3_810_000, Status: storage.SegmentGap},
	}, map[string]string{storage.TrackMicrophone: "Yo", storage.TrackOutput: "Ana"})
	want := "Reunión: 2026-11-16 10:30\nIdioma: español\n\n" +
		"[00:00:12] Yo: Buenos días. ¿Empezamos?\n" +
		"[00:00:16] Ana: Sí, comparto pantalla.\n" +
		"[01:02:05–01:02:25] (sin transcribir: falló la transcripción, Ana)\n" +
		"[01:03:20–01:03:30] (sin transcribir: audio no capturado, Yo)\n"
	if got != want {
		t.Errorf("export mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestNameInferenceTranscript(t *testing.T) {
	segments := []storage.TranscriptSegment{
		{Track: storage.TrackMicrophone, Status: storage.SegmentOK, Text: "Hola Pedro"},
		{Track: storage.TrackOutput, Status: storage.SegmentOK, SpeakerKey: "spk_0", Text: "Hola, soy Pedro"},
		{Track: storage.TrackOutput, Status: storage.SegmentOK, SpeakerKey: "", Text: "sin diarizar todavía"},
		{Track: storage.TrackOutput, Status: storage.SegmentFailed, SpeakerKey: "spk_0", Text: ""},
		{Track: storage.TrackOutput, Status: storage.SegmentOK, SpeakerKey: "spk_1", Text: "   "}, // blank text, must be skipped
	}
	got := NameInferenceTranscript(segments)
	want := "Yo: Hola Pedro\nspk_0: Hola, soy Pedro\ngenerico: sin diarizar todavía\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
