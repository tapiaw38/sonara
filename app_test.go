package main

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

// Node IDs and serials are deliberately different: pw-record --target takes
// object.serial or node.name, never the pw-dump node id.
func node(name string, id, serial int, state string) string {
	return fmt.Sprintf(`{"type":"PipeWire:Interface:Node","id":%d,"info":{"state":%q,"props":{"media.class":"Stream/Output/Audio","application.name":%q,"object.serial":%d}}}`,
		id, state, name, serial)
}

func nodeWithMedia(name, media string, id, serial int, state string) string {
	return fmt.Sprintf(`{"type":"PipeWire:Interface:Node","id":%d,"info":{"state":%q,"props":{"media.class":"Stream/Output/Audio","application.name":%q,"media.name":%q,"object.serial":%d}}}`,
		id, state, name, media, serial)
}

// A real incident went deaf for 12 minutes in exactly this shape: several
// Chrome tabs with audio, nothing named "teams" among them (Teams Web is a
// Chrome tab, so it never is), and no fallback once the single-match rule came
// up empty. The fix is to guess — running wins, then the newest serial — since
// a wrong guess shows up as nonsense in the transcript and gets corrected by
// hand, while silence announces nothing at all.
func TestPickChromeTargetGuessesAmongAmbiguousBrowserStreams(t *testing.T) {
	dump := "[" + strings.Join([]string{
		node("Firefox", 139, 301, "running"),
		node("Google Chrome", 173, 328, "idle"),
		node("Google Chrome", 188, 461, "running"),
		node("Google Chrome", 181, 402, "running"),
	}, ",") + "]"

	target, err := pickChromeTarget([]byte(dump))
	if err != nil || target != "461" {
		t.Errorf("target = %q, err = %v, want the newest running serial, 461", target, err)
	}
}

func TestPickChromeTargetReturnsSerialNotNodeID(t *testing.T) {
	dump := "[" + node("Google Chrome", 173, 328, "running") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil {
		t.Fatalf("pickChromeTarget: %v", err)
	}
	if target == "173" {
		t.Fatal("returned pw-dump node id instead of object.serial")
	}
	if target != "328" {
		t.Errorf("target = %q, want serial 328", target)
	}
}

func TestPickChromeTargetFallsBackToIdleChrome(t *testing.T) {
	dump := "[" + node("Google Chrome", 173, 328, "idle") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil || target != "328" {
		t.Errorf("target = %q, err = %v, want serial 328", target, err)
	}
}

func TestPickChromeTargetWithoutMeetingApp(t *testing.T) {
	dump := "[" + node("VLC", 139, 301, "running") + "]"
	if _, err := pickChromeTarget([]byte(dump)); err == nil {
		t.Error("expected an error when no supported meeting app has an output stream")
	}
}

func TestPickChromeTargetSupportsTeams(t *testing.T) {
	dump := "[" + node("Microsoft Teams", 211, 504, "running") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil || target != "504" {
		t.Errorf("target = %q, err = %v, want Teams serial 504", target, err)
	}
}

// media.name is not used to decide: in production Chrome reports it as the
// generic "Playback" regardless of the tab, so a title here is only ever
// synthetic test data. The guess goes by running state and recency instead.
func TestPickChromeTargetGuessesTheNewestRunningTabWhenNamesCannotDecide(t *testing.T) {
	dump := "[" + strings.Join([]string{
		nodeWithMedia("Google Chrome", "Google Meet", 211, 504, "running"),
		nodeWithMedia("Google Chrome", "YouTube Music", 212, 600, "running"),
	}, ",") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil || target != "600" {
		t.Errorf("target = %q, err = %v, want the newer tab, 600, even though it is not the meeting", target, err)
	}
}

func TestPickChromeTargetPrefersDedicatedTeamsOverBrowserMusic(t *testing.T) {
	dump := "[" + strings.Join([]string{
		nodeWithMedia("Microsoft Teams", "Teams call", 211, 504, "running"),
		nodeWithMedia("Google Chrome", "YouTube Music", 212, 600, "running"),
	}, ",") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil || target != "504" {
		t.Errorf("target = %q, err = %v, want Teams serial 504", target, err)
	}
}

// Running outranks idle even when the idle stream's title says "Meet": a tab
// that merely exists loses to one that is actually live, and the title is
// never consulted either way.
func TestPickChromeTargetPrefersRunningOverIdleRegardlessOfTitle(t *testing.T) {
	dump := "[" + strings.Join([]string{
		nodeWithMedia("Google Chrome", "Google Meet", 211, 504, "idle"),
		nodeWithMedia("Google Chrome", "YouTube Music", 212, 600, "running"),
	}, ",") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil || target != "600" {
		t.Errorf("target = %q, err = %v, want the running tab, 600", target, err)
	}
}

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
	app.holdPendingAudio(oldSession, make([]byte, meetSampleRate*2))
	if len(app.sessionHistory) != 0 || app.voiceReply != "" || len(app.pendingAudio) != 0 {
		t.Fatal("late voice result leaked into the new session")
	}
}

func TestSessionHistoryUsesStoredMessages(t *testing.T) {
	app := NewApp()
	app.mu.Lock()
	for i := range sessionHistoryLimit + 2 {
		app.appendSessionMessageLocked("user", fmt.Sprintf("message-%d", i))
	}
	context := app.buildSessionContextLocked()
	length := len(app.sessionHistory)
	app.mu.Unlock()
	if length != sessionHistoryLimit || strings.Contains(context, "message-0\n") || !strings.Contains(context, "message-11") {
		t.Errorf("stored=%d, context=%q", length, context)
	}
}

func TestTruncateContextPreservesUTF8(t *testing.T) {
	got := truncateContext("áéíó", 3)
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "á\n") {
		t.Errorf("truncateContext broke UTF-8: %q", got)
	}
}

func TestNoiseFloorRisesAboveAmbientHiss(t *testing.T) {
	quiet := &noiseFloor{}
	if got := quiet.threshold(); got != meetNoiseFloor {
		t.Errorf("threshold with no samples = %v, want the absolute floor %v", got, meetNoiseFloor)
	}

	// A silent stream must not end up treating every frame as voice.
	for range meetNoiseWindow {
		quiet.observe(0)
	}
	if got := quiet.threshold(); got != meetNoiseFloor {
		t.Errorf("threshold on a silent stream = %v, want %v", got, meetNoiseFloor)
	}

	// A hissy call lifts the bar, so the hiss itself stops counting as voice.
	noisy := &noiseFloor{}
	for range meetNoiseWindow {
		noisy.observe(0.02)
	}
	want := 0.02 * meetNoiseMargin
	if got := noisy.threshold(); math.Abs(got-want) > 1e-9 {
		t.Errorf("threshold on a noisy stream = %v, want %v", got, want)
	}
	if noisy.threshold() <= 0.02 {
		t.Error("threshold must sit above the ambient level it measured")
	}
}

func TestNoiseFloorKeepsOnlyRecentFrames(t *testing.T) {
	floor := &noiseFloor{}
	for range meetNoiseWindow * 3 {
		floor.observe(0.05)
	}
	if len(floor.quiet) != meetNoiseWindow {
		t.Errorf("window = %d frames, want %d", len(floor.quiet), meetNoiseWindow)
	}
	// The room went quiet: the old loud samples must age out of the estimate.
	for range meetNoiseWindow {
		floor.observe(0)
	}
	if got := floor.threshold(); got != meetNoiseFloor {
		t.Errorf("threshold after the noise stopped = %v, want %v", got, meetNoiseFloor)
	}
}

func TestTrimTrailingSilence(t *testing.T) {
	frames := func(n int) []byte { return make([]byte, n*meetFrameBytes) }

	got := trimTrailingSilence(frames(30), meetSilenceFrames)
	wantFrames := 30 - (meetSilenceFrames - meetKeepSilence)
	if len(got)/meetFrameBytes != wantFrames {
		t.Errorf("trimmed to %d frames, want %d", len(got)/meetFrameBytes, wantFrames)
	}

	// A segment cut by the 15s cap has no trailing silence to drop.
	if got := trimTrailingSilence(frames(30), 0); len(got)/meetFrameBytes != 30 {
		t.Errorf("segment without silence trimmed to %d frames, want 30", len(got)/meetFrameBytes)
	}

	// Trimming must never take a segment below the minimum length.
	short := frames(meetMinimumFrames + 1)
	if got := trimTrailingSilence(short, meetSilenceFrames); len(got) != len(short) {
		t.Errorf("short segment trimmed to %d frames, want %d", len(got)/meetFrameBytes, meetMinimumFrames+1)
	}
}

// The 15s ceiling must not end the capture: a long question has to stay one
// continuous recording, with a short overlap so no word is lost at the seam.
func TestTailFramesCarriesTheOverlap(t *testing.T) {
	segment := make([]byte, 0, meetMaximumFrames*meetFrameBytes)
	for i := range meetMaximumFrames {
		frame := make([]byte, meetFrameBytes)
		frame[0] = byte(i)
		segment = append(segment, frame...)
	}
	tail := tailFrames(segment, meetOverlapFrames)
	if len(tail) != meetOverlapFrames*meetFrameBytes {
		t.Fatalf("tail is %d frames, want %d", len(tail)/meetFrameBytes, meetOverlapFrames)
	}
	// It has to be the *last* frames, not the first: the seam is at the end.
	if tail[0] != byte(meetMaximumFrames-meetOverlapFrames) {
		t.Errorf("tail starts at frame %d, want %d", tail[0], meetMaximumFrames-meetOverlapFrames)
	}
	// A segment shorter than the overlap is returned whole rather than panicking.
	if got := tailFrames(segment[:meetFrameBytes], meetOverlapFrames); len(got) != meetFrameBytes {
		t.Errorf("short segment returned %d bytes, want %d", len(got), meetFrameBytes)
	}
}

func TestContinuationChunksDeduplicateOverlap(t *testing.T) {
	first := make([]byte, 3*meetFrameBytes)
	second := append(append([]byte(nil), first[len(first)-meetOverlapFrames*meetFrameBytes:]...), make([]byte, meetFrameBytes)...)
	joined := appendPCMChunk(first, second)
	if len(joined) != 4*meetFrameBytes {
		t.Errorf("joined audio = %d frames, want 4", len(joined)/meetFrameBytes)
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
	if !strings.Contains(english, translationMarker) {
		t.Error("English prompt must ask for the marked translation line")
	}
	if !strings.Contains(english, replyTranslationMarker) {
		t.Error("English prompt must ask for the suggested answer to be translated too")
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
	app.holdPendingAudio(app.sessionGeneration, make([]byte, meetSampleRate*2)) // 1s of silence-valued PCM

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
	wav := wavFromPCM(make([]byte, 3200))
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
