package main

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
)

// node ids and serials are deliberately different: pw-record resolves --target
// by id, while the newest-stream ranking is based on object.serial.
func node(name string, id, serial int, state string) string {
	return fmt.Sprintf(`{"type":"PipeWire:Interface:Node","id":%d,"info":{"state":%q,"props":{"media.class":"Stream/Output/Audio","application.name":%q,"object.serial":%d}}}`,
		id, state, name, serial)
}

func TestPickChromeTargetPrefersRunningNewestChromeNode(t *testing.T) {
	dump := "[" + strings.Join([]string{
		node("Firefox", 139, 301, "running"),
		node("Google Chrome", 173, 328, "idle"),
		node("Google Chrome", 188, 461, "running"),
		node("Google Chrome", 181, 402, "running"),
	}, ",") + "]"

	target, err := pickChromeTarget([]byte(dump))
	if err != nil {
		t.Fatalf("pickChromeTarget: %v", err)
	}
	if target != "188" {
		t.Errorf("target = %q, want 188 (node id of the newest running Chrome node)", target)
	}
}

func TestPickChromeTargetReturnsNodeIDNotSerial(t *testing.T) {
	dump := "[" + node("Google Chrome", 173, 328, "running") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil {
		t.Fatalf("pickChromeTarget: %v", err)
	}
	if target == "328" {
		t.Fatal("returned object.serial; pw-record --target resolves node ids and records silence for an unknown serial")
	}
	if target != "173" {
		t.Errorf("target = %q, want 173", target)
	}
}

func TestPickChromeTargetFallsBackToIdleChrome(t *testing.T) {
	dump := "[" + node("Google Chrome", 173, 328, "idle") + "]"
	target, err := pickChromeTarget([]byte(dump))
	if err != nil || target != "173" {
		t.Errorf("target = %q, err = %v, want 173", target, err)
	}
}

func TestPickChromeTargetWithoutChrome(t *testing.T) {
	dump := "[" + node("Firefox", 139, 301, "running") + "]"
	if _, err := pickChromeTarget([]byte(dump)); err == nil {
		t.Error("expected an error when Chrome has no output stream")
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

func TestParseSSEToken(t *testing.T) {
	cases := []struct {
		line  string
		token string
		done  bool
	}{
		{`data: "hola"`, "hola", false},
		{`data: " con espacios "`, " con espacios ", false},
		{`data: "linea\nrota"`, "linea\nrota", false}, // newlines survive JSON decoding
		{`data: [DONE]`, "", true},
		{`data: "[DONE]"`, "", true},
		{`data: {"type":"audio","url":"http://x/a.mp3"}`, "", false},
		{`event: ping`, "", false},
		{``, "", false},
		{`data: sin-json`, "sin-json", false},
	}
	for _, want := range cases {
		token, done := parseSSEToken(want.line)
		if token != want.token || done != want.done {
			t.Errorf("parseSSEToken(%q) = (%q, %v), want (%q, %v)", want.line, token, done, want.token, want.done)
		}
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
	app.processMeetSegment(make([]byte, meetSampleRate*2)) // 1s of silence-valued PCM

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
