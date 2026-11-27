package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"sonara/internal/audio"
	"sonara/internal/storage"
)

func TestSetAssistantModeSupportsAvailableModes(t *testing.T) {
	app := NewApp()
	for _, mode := range []string{"interview", "daily", "coding", "coaching"} {
		if got := app.SetAssistantMode(mode); got != mode {
			t.Errorf("SetAssistantMode(%q) = %q", mode, got)
		}
	}
	if got := app.SetAssistantMode("unknown"); got != "interview" {
		t.Errorf("SetAssistantMode(unknown) = %q, want interview", got)
	}
}

func TestNewSessionRejectsLateVoiceResult(t *testing.T) {
	app := NewApp()
	oldSession := app.sessionGeneration
	app.replySequence = 1
	app.NewSession()
	app.finishVoiceResponse(oldSession, 1, "pregunta anterior", "respuesta anterior", nil)
	app.holdPendingAudio(oldSession, make([]byte, audio.SampleRate*2))
	if len(app.sessionHistory) != 0 || app.voiceReply != "" || len(app.pendingAudio) != 0 {
		t.Fatal("late voice result leaked into the new session")
	}
}

func TestSessionHistoryUsesStoredMessages(t *testing.T) {
	app := NewApp()
	app.mu.Lock()
	long := strings.Repeat("x", 400)
	for i := range 20 {
		app.appendSessionMessageLocked("user", fmt.Sprintf("message-%d %s", i, long))
	}
	app.memorySummary = "summary of the early talk"
	context := app.buildSessionContextLocked()
	window, pending := len(app.sessionHistory), len(app.memoryPending)
	app.mu.Unlock()
	// The window holds the newest turns, the rest waits for the summary, and the
	// context carries the summary plus the latest turn, never the oldest.
	if window >= 20 || window+pending != 20 || strings.Contains(context, "message-0 ") ||
		!strings.Contains(context, "message-19") || !strings.Contains(context, "summary of the early talk") {
		t.Errorf("window=%d pending=%d context=%q", window, pending, context)
	}
}

func TestTrimTrailingSilence(t *testing.T) {
	frames := func(n int) []byte { return make([]byte, n*audio.FrameBytes) }

	got := audio.TrimTrailingSilence(frames(30), meetSilenceFrames, meetKeepSilence, meetMinimumFrames)
	wantFrames := 30 - (meetSilenceFrames - meetKeepSilence)
	if len(got)/audio.FrameBytes != wantFrames {
		t.Errorf("trimmed to %d frames, want %d", len(got)/audio.FrameBytes, wantFrames)
	}

	// A segment cut by the 15s cap has no trailing silence to drop.
	if got := audio.TrimTrailingSilence(frames(30), 0, meetKeepSilence, meetMinimumFrames); len(got)/audio.FrameBytes != 30 {
		t.Errorf("segment without silence trimmed to %d frames, want 30", len(got)/audio.FrameBytes)
	}

	// Trimming must never take a segment below the minimum length.
	short := frames(meetMinimumFrames + 1)
	if got := audio.TrimTrailingSilence(short, meetSilenceFrames, meetKeepSilence, meetMinimumFrames); len(got) != len(short) {
		t.Errorf("short segment trimmed to %d frames, want %d", len(got)/audio.FrameBytes, meetMinimumFrames+1)
	}
}

// The 15s ceiling must not end the capture: a long question has to stay one
// continuous recording, with a short overlap so no word is lost at the seam.
func TestTailFramesCarriesTheOverlap(t *testing.T) {
	segment := make([]byte, 0, meetMaximumFrames*audio.FrameBytes)
	for i := range meetMaximumFrames {
		frame := make([]byte, audio.FrameBytes)
		frame[0] = byte(i)
		segment = append(segment, frame...)
	}
	tail := audio.TailFrames(segment, meetOverlapFrames)
	if len(tail) != meetOverlapFrames*audio.FrameBytes {
		t.Fatalf("tail is %d frames, want %d", len(tail)/audio.FrameBytes, meetOverlapFrames)
	}
	// It has to be the *last* frames, not the first: the seam is at the end.
	if tail[0] != byte(meetMaximumFrames-meetOverlapFrames) {
		t.Errorf("tail starts at frame %d, want %d", tail[0], meetMaximumFrames-meetOverlapFrames)
	}
	// A segment shorter than the overlap is returned whole rather than panicking.
	if got := audio.TailFrames(segment[:audio.FrameBytes], meetOverlapFrames); len(got) != audio.FrameBytes {
		t.Errorf("short segment returned %d bytes, want %d", len(got), audio.FrameBytes)
	}
}

func TestContinuationChunksDeduplicateOverlap(t *testing.T) {
	first := make([]byte, 3*audio.FrameBytes)
	second := append(append([]byte(nil), first[len(first)-meetOverlapFrames*audio.FrameBytes:]...), make([]byte, audio.FrameBytes)...)
	joined := appendPCMChunk(first, second)
	if len(joined) != 4*audio.FrameBytes {
		t.Errorf("joined audio = %d frames, want 4", len(joined)/audio.FrameBytes)
	}
	if got := joinTranscriptPieces("What is your experience with", "with Go and Kubernetes?"); got != "What is your experience with Go and Kubernetes?" {
		t.Errorf("merged transcript = %q", got)
	}
}

// A blocked capture loop goes deaf, so queueing must never block: pw-record's
// pipe only holds about two seconds of audio.
func TestQueueSegmentNeverBlocks(t *testing.T) {
	segments := make(chan meetSegment, meetQueuedSegments)
	for i := range meetQueuedSegments {
		if !queueSegment(segments, meetSegment{pcm: []byte{byte(i)}}) {
			t.Fatalf("segment %d was dropped while the queue had room", i)
		}
	}
	if queueSegment(segments, meetSegment{pcm: []byte{9}}) {
		t.Error("queueSegment accepted a segment past the queue size")
	}
	// The copy matters: the capture loop reuses its buffer for the next segment.
	buffer := []byte{1, 2, 3}
	queued := make(chan meetSegment, 1)
	queueSegment(queued, meetSegment{pcm: buffer})
	buffer[0] = 99
	if got := <-queued; got.pcm[0] != 1 {
		t.Errorf("queued segment aliases the caller's buffer: got %d, want 1", got.pcm[0])
	}
}

func TestVoicePromptAsksForASuggestionNotAnAnswer(t *testing.T) {
	for _, language := range []string{"es", "en"} {
		prompt := voicePrompt(language, "How old are you")
		if !strings.Contains(prompt, suggestionBrief) {
			t.Errorf("%s prompt lost the suggestion framing, Gillie would answer about itself", language)
		}
		if !strings.HasSuffix(prompt, "How old are you") {
			t.Errorf("%s prompt does not end with the transcript", language)
		}
	}
	english := voicePrompt("en", "hi")
	if strings.Contains(english, translationMarker) {
		t.Error("the translation of what they said is its own parallel request now; the reply prompt must not ask for it")
	}
	if !strings.Contains(english, "Respondé en inglés.") {
		t.Error("English prompt must ask for the suggestion in English")
	}
	if strings.Contains(voicePrompt("es", "hola"), translationMarker) {
		t.Error("Spanish audio needs no translation line")
	}
}

func TestSplitTranslation(t *testing.T) {
	cases := []struct {
		name, reply, translation, rest string
	}{
		{"marked reply", "TRADUCCIÓN: ¿Qué edad tenés?\n\nNo tengo edad.", "¿Qué edad tenés?", "No tengo edad."},
		{"no marker", "Hola, ¿en qué puedo ayudarte?", "", "Hola, ¿en qué puedo ayudarte?"},
		{"lowercase marker", "traducción: Hola\n\nRespuesta", "Hola", "Respuesta"},
		{"leading blank lines", "\n\nTRADUCCIÓN: Hola\n\nRespuesta", "Hola", "Respuesta"},
		{"multiline reply", "TRADUCCIÓN: Hola\n\nUno\nDos", "Hola", "Uno\nDos"},
		{"translation still streaming", "TRADUCCIÓN: ¿Qué edad", "¿Qué edad", ""},
		{"empty", "", "", ""},
	}
	for _, want := range cases {
		t.Run(want.name, func(t *testing.T) {
			translation, rest := splitTranslation(want.reply)
			if translation != want.translation || rest != want.rest {
				t.Errorf("splitTranslation(%q) = (%q, %q), want (%q, %q)",
					want.reply, translation, rest, want.translation, want.rest)
			}
		})
	}
}

// A reply arriving token by token must never flash the marker in the reply
// panel, and must settle on the same split as the complete string.
func TestSplitTranslationWhileStreaming(t *testing.T) {
	full := "TRADUCCIÓN: ¿Qué edad tenés?\n\nNo tengo edad, soy una IA."
	raw := ""
	for _, char := range full {
		raw += string(char)
		translation, rest := splitTranslation(raw)
		if strings.Contains(strings.ToUpper(translation+rest), "TRADUCC") {
			t.Fatalf("marker leaked at %q: translation=%q rest=%q", raw, translation, rest)
		}
	}
	translation, rest := splitTranslation(raw)
	if translation != "¿Qué edad tenés?" || rest != "No tengo edad, soy una IA." {
		t.Errorf("final split = (%q, %q)", translation, rest)
	}
}

func TestAudioLevelIsNormalisedAndClamped(t *testing.T) {
	app := NewApp()
	if got := app.AudioLevel(); got != 0 {
		t.Errorf("idle level = %v, want 0", got)
	}
	app.setAudioLevel(meetLoudFrame / 2)
	if got := app.AudioLevel(); got != 0.5 {
		t.Errorf("half-loud level = %v, want 0.5", got)
	}
	app.setAudioLevel(meetLoudFrame * 4)
	if got := app.AudioLevel(); got != 1 {
		t.Errorf("clipping level = %v, want 1", got)
	}
}

func TestManualSendHoldsSegmentUntilRequested(t *testing.T) {
	app := NewApp()
	app.SetManualSend(true)
	app.holdPendingAudio(app.sessionGeneration, make([]byte, audio.SampleRate*2)) // 1s of silence-valued PCM

	if got := app.ListeningStatus().PendingSeconds; got != 1 {
		t.Errorf("pendingSeconds = %v, want 1", got)
	}
	if !app.DiscardPendingSegment() {
		t.Error("discard should report success")
	}
	if got := app.ListeningStatus().PendingSeconds; got != 0 {
		t.Errorf("pendingSeconds after discard = %v, want 0", got)
	}
	if app.SendPendingSegment() {
		t.Error("send should report false when nothing is pending")
	}
}

func TestWavFromPCMHeader(t *testing.T) {
	wav := audio.WAV(make([]byte, 3200))
	if !bytes.Equal(wav[0:4], []byte("RIFF")) {
		t.Errorf("riff id = %q", wav[0:4])
	}
	if !bytes.Equal(wav[8:12], []byte("WAVE")) {
		t.Errorf("wave id = %q", wav[8:12])
	}
	if !bytes.Equal(wav[12:16], []byte("fmt ")) {
		t.Errorf("fmt chunk id = %q", wav[12:16])
	}
	if !bytes.Equal(wav[36:40], []byte("data")) {
		t.Errorf("data chunk id = %q", wav[36:40])
	}
	if len(wav) != 44+3200 {
		t.Errorf("length = %d", len(wav))
	}
}

func TestOverlayPlanFloatsTiledWindowBeforeOverlay(t *testing.T) {
	cmds, floated := overlayPlan(false, false, false)
	if len(cmds) != 2 || cmds[0] != "togglefloating" || cmds[1] != "toggleoverlay" {
		t.Fatalf("una ventana tileada debe flotarse antes del overlay, dio %v", cmds)
	}
	if !floated {
		t.Fatal("debe recordar que la flotamos nosotros")
	}
}

func TestOverlayPlanKeepsUserFloatingWindowFloating(t *testing.T) {
	cmds, floated := overlayPlan(false, true, false)
	if len(cmds) != 1 || cmds[0] != "toggleoverlay" {
		t.Fatalf("una ventana ya flotante no debe re-flotarse, dio %v", cmds)
	}
	if floated {
		t.Fatal("no la flotamos nosotros, no hay que devolverla a tiled")
	}
}

func TestOverlayPlanRestoresTilingOnlyIfWeFloated(t *testing.T) {
	cmds, floated := overlayPlan(true, true, true)
	if len(cmds) != 2 || cmds[1] != "togglefloating" {
		t.Fatalf("debe devolver a tiled lo que flotamos, dio %v", cmds)
	}
	if floated {
		t.Fatal("tras apagar ya no queda nada flotado por nosotros")
	}

	cmds, _ = overlayPlan(true, true, false)
	if len(cmds) != 1 || cmds[0] != "toggleoverlay" {
		t.Fatalf("no debe des-flotar una ventana que el usuario floto, dio %v", cmds)
	}
}

// A pause closes a turn. Unbroken audio also has to close it, or a stream that
// never goes quiet is transcribed for as long as it plays — which is what a
// shared video or hold music did: twelve 15s parts in a row, a third of them
// coming back with no speech in them at all.
func TestSegmentCloseBoundsUnbrokenAudio(t *testing.T) {
	if final, hold := segmentClose(true, 0); !final || hold {
		t.Errorf("a real pause = (final %v, hold %v), want (true, false)", final, hold)
	}
	// Mid-intervention parts stay open so a long question is one capture.
	for part := uint64(0); part+1 < meetMaxParts; part++ {
		if final, hold := segmentClose(false, part); final || hold {
			t.Errorf("part %d = (final %v, hold %v), want both false", part, final, hold)
		}
	}
	// The last allowed part must close the turn, or the reply never arrives,
	// and must hold capture so the same audio is not picked straight back up.
	if final, hold := segmentClose(false, meetMaxParts-1); !final || !hold {
		t.Errorf("the capped part = (final %v, hold %v), want (true, true)", final, hold)
	}
	// A pause always wins over the cap: it is a genuine end of turn.
	if final, hold := segmentClose(true, meetMaxParts+5); !final || hold {
		t.Errorf("a pause past the cap = (final %v, hold %v), want (true, false)", final, hold)
	}
}

// The translation of what they said streams on its own request. Finishing the
// reply (which carries no translation line any more) must not blank it.
func TestFinishVoiceResponseKeepsTheParallelTranslation(t *testing.T) {
	app := NewApp()
	app.replySequence = 1
	app.translation = "¿Qué edad tenés?"
	app.finishVoiceResponse(app.sessionGeneration, 1, "How old are you", "I'm 30.\n\nES: Tengo 30.", nil)
	if app.translation != "¿Qué edad tenés?" {
		t.Errorf("translation = %q, the reply finishing must not clobber it", app.translation)
	}
	if !strings.HasPrefix(app.voiceReply, "I'm 30.") {
		t.Errorf("voiceReply = %q", app.voiceReply)
	}
}

func TestSinceMS(t *testing.T) {
	if got := sinceMS(time.Time{}, time.Now()); got != -1 {
		t.Errorf("unrecorded start = %d, want -1", got)
	}
	start := time.Now()
	if got := sinceMS(start, start.Add(1500*time.Millisecond)); got != 1500 {
		t.Errorf("got %d, want 1500", got)
	}
}

func TestNewSessionInheritsLastProfile(t *testing.T) {
	app, database := testMeetingApp(t)
	app.mu.RLock()
	first := app.sessionID
	app.mu.RUnlock()
	if err := database.SaveSession(first, "interview", "es"); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveSessionProfile(first, storage.SessionProfile{Scenario: "Técnica", Company: "Acme", Role: "Go dev"}); err != nil {
		t.Fatal(err)
	}
	app.NewSession()
	app.mu.RLock()
	second, profile := app.sessionID, app.sessionProfile
	app.mu.RUnlock()
	stored, err := app.GetSessionProfile(second)
	if second == first || profile.Company != "Acme" || err != nil || stored.Role != "Go dev" {
		t.Fatalf("new session %q got %+v, stored %+v (%v)", second, profile, stored, err)
	}
}

// A full notes field reaches the prompt whole: the shared context budget does
// not cut the profile.
func TestSessionNotesKeepFullLengthInContext(t *testing.T) {
	notes := strings.Repeat("á", storage.SessionNotesMaxChars-4) + " fin"
	a := &App{sessionProfile: storage.SessionProfile{Notes: notes}}
	if !strings.Contains(a.buildSessionContextLocked(), notes) {
		t.Fatal("notes cut from the session context")
	}
}

func TestMemoryRoleInPractice(t *testing.T) {
	if memoryRole("practice", "user") != "me (what I said out loud)" || memoryRole("practice", "assistant") != "interviewer" ||
		memoryRole("interview", "user") != "user" || memoryRole("interview", "me") != "me (what I said out loud)" {
		t.Fatal("practice answers must read as the candidate's own words")
	}
}

func TestMeetRecorderIdleWithoutMeeting(t *testing.T) {
	if _, _, ok := (meetRecorder{&App{}}).ActiveRecording(); ok {
		t.Fatal("no meeting running: the Meet bridge must have nothing to attach to")
	}
}

func TestListenMeetAudioLogsStartError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	dump := `#!/bin/sh
printf '%s\n' '[{"type":"PipeWire:Interface:Node","id":1,"info":{"state":"running","props":{"media.class":"Stream/Output/Audio","application.name":"Google Chrome","object.serial":12}}}]'
`
	if err := os.WriteFile(filepath.Join(dir, "pw-dump"), []byte(dump), 0o700); err != nil {
		t.Fatal(err)
	}
	original := logf
	t.Cleanup(func() { logf = original })
	var startErr error
	logf = func(format string, args ...any) {
		if strings.HasPrefix(format, "pw-record could not attach") {
			startErr, _ = args[1].(error)
		}
	}
	a := NewApp()
	a.listenMeetAudio(context.Background())
	if !errors.Is(startErr, exec.ErrNotFound) {
		t.Fatalf("logged start error = %v, want missing pw-record", startErr)
	}
}

func TestCaptureScreenUsesUniquePendingFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("CAPTURE_TEST_PATHS", filepath.Join(dir, "paths"))
	grim := `#!/bin/sh
printf '%s\n' "$1" >> "$CAPTURE_TEST_PATHS"
printf 'image' > "$1"
exit "${CAPTURE_TEST_EXIT:-0}"
`
	if err := os.WriteFile(filepath.Join(dir, "grim"), []byte(grim), 0o700); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := a.CaptureScreen()
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "paths"))
	if err != nil {
		t.Fatal(err)
	}
	paths := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Fatalf("pending paths = %q, want two distinct paths", paths)
	}

	t.Setenv("CAPTURE_TEST_EXIT", "1")
	if _, err := a.CaptureScreen(); err == nil {
		t.Fatal("expected grim failure")
	}
	path, err := capturePath()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("capture directory = %v, want only latest.png", entries)
	}
}

func TestNewSessionID(t *testing.T) {
	first, second := newSessionID(), newSessionID()
	if first == second {
		t.Fatal("consecutive session IDs collided")
	}
	format := regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}-[0-9a-f]{6}$`)
	for _, id := range []string{first, second} {
		if !format.MatchString(id) {
			t.Errorf("session ID %q has an unexpected format", id)
		}
	}
}
