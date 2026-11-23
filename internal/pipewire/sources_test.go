package pipewire

import (
	"fmt"
	"strings"
	"testing"
)

// A meeting app replaces its output stream when a call starts, reconnects or
// renegotiates, so the chosen serial disappears while the call is still up.
// Re-latching by application is what keeps capture alive through that.
func TestAdoptPreferredApp(t *testing.T) {
	chrome := Source{Target: "501", App: "google chrome chrome"}
	firefox := Source{Target: "502", App: "firefox firefox"}
	otherChrome := Source{Target: "503", App: "google chrome chrome"}

	got, err := AdoptPreferredApp([]Source{firefox, chrome}, "google chrome chrome")
	if err != nil || got != "501" {
		t.Errorf("unique match = (%q, %v), want 501", got, err)
	}

	// Two streams from the same app could be the call or another tab. Guessing
	// wrong means transcribing the wrong audio, so it must refuse.
	if _, err := AdoptPreferredApp([]Source{chrome, otherChrome}, "google chrome chrome"); err == nil {
		t.Error("ambiguous application must not be adopted")
	}

	if _, err := AdoptPreferredApp([]Source{firefox}, "google chrome chrome"); err == nil {
		t.Error("absent application must not be adopted")
	}

	// Nothing was ever chosen explicitly, so there is nothing to re-latch onto.
	if _, err := AdoptPreferredApp([]Source{chrome}, ""); err == nil {
		t.Error("empty preference must not be adopted")
	}
}

// Teams Web runs inside Chrome, so its process name is "Google Chrome" like
// any other tab: Meeting, which only fires on the literal name "teams", never
// fires for it. A real incident went silent for 12 minutes in exactly this
// situation — several Chrome streams, none of them named — because
// chooseAudioSource had no fallback once the Meeting shortcut came up empty.
func TestChooseAudioSourceGuessesAmongUnnamedChromeStreams(t *testing.T) {
	older := Source{Target: "100", App: "google chrome chrome", Running: true}
	newer := Source{Target: "200", App: "google chrome chrome", Running: true}
	idleNewest := Source{Target: "300", App: "google chrome chrome", Running: false}

	got, guessed, err := ChooseStream([]Source{older, newer})
	if err != nil || got != "200" || !guessed {
		t.Errorf("two unnamed Chrome streams = (%q, %v, %v), want (200, guessed, nil)", got, guessed, err)
	}

	// Idle must lose to running even when it is the more recently opened
	// stream: a call that is actually live outranks a tab that merely exists.
	got, guessed, err = ChooseStream([]Source{older, newer, idleNewest})
	if err != nil || got != "200" || !guessed {
		t.Errorf("with an idle newer stream = (%q, %v, %v), want (200, guessed, nil)", got, guessed, err)
	}

	// A literally named Teams stream still wins outright over the guess, and
	// is reported as confident rather than guessed.
	teams := Source{Target: "400", App: "microsoft teams teams", Running: true, Meeting: true}
	got, guessed, err = ChooseStream([]Source{older, newer, teams})
	if err != nil || got != "400" || guessed {
		t.Errorf("with a named Teams stream present = (%q, %v, %v), want (400, not guessed, nil)", got, guessed, err)
	}
}

// Nothing running anywhere is a real "waiting for audio" state, not a bug:
// guessing has to stop somewhere, and a stream with no numeric serial cannot
// be ranked by recency at all.
func TestNewestRunningSourceRequiresARankableCandidate(t *testing.T) {
	if _, err := newestRunningSource(nil); err == nil {
		t.Error("no candidates must fail")
	}
	named := Source{Target: "pulse-sink-name"} // no object.serial, fell back to a name
	if _, err := newestRunningSource([]Source{named}); err == nil {
		t.Error("a non-numeric target must not be guessable")
	}
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

func TestParseMicrophonesIgnoresOutputsAndMonitors(t *testing.T) {
	dump := `[
	 {"type":"PipeWire:Interface:Node","info":{"state":"suspended","props":{"media.class":"Audio/Source","node.name":"alsa_input.mic","node.description":"Micrófono interno","object.serial":2152}}},
	 {"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"media.class":"Audio/Sink","node.name":"alsa_output.speakers","object.serial":77}}},
	 {"type":"PipeWire:Interface:Node","info":{"state":"running","props":{"media.class":"Stream/Output/Audio","application.name":"Chrome","node.name":"chrome","object.serial":501}}},
	 {"type":"PipeWire:Interface:Node","info":{"state":"idle","props":{"media.class":"Audio/Source/Virtual","node.name":"virtual","object.serial":9}}}
	]`
	got, err := parseMicrophones([]byte(dump))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v; want exactly the real microphone", got, err)
	}
	if got[0].Target != "2152" || got[0].App != "alsa_input.mic" || got[0].Label != "Micrófono interno" || got[0].Kind != KindInput {
		t.Errorf("unexpected microphone %+v", got[0])
	}
	outputs, _ := parseAudioSources([]byte(dump))
	if len(outputs) != 1 || outputs[0].Kind != KindOutput {
		t.Errorf("outputs = %+v, want only the browser stream, tagged output", outputs)
	}
}

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

// TestFindMicrophoneAppMatchesByStableNameNotSerial is a regression test for
// a real bug: SetMicrophone used to compare against AudioSource.Target (the
// PipeWire serial), but the UI's <option> sends AudioSource.App (the stable
// node name) — so the match never succeeded and every selection reported
// "el micrófono ya no está disponible" even with the microphone present.
func TestFindMicrophoneAppMatchesByStableNameNotSerial(t *testing.T) {
	sources := []Source{
		{Target: "2152", App: "alsa_input.pci-0000_00_1f.3.analog-stereo", Label: "Micrófono interno"},
	}
	got, err := FindMicrophoneApp(sources, "alsa_input.pci-0000_00_1f.3.analog-stereo")
	if err != nil || got != "alsa_input.pci-0000_00_1f.3.analog-stereo" {
		t.Fatalf("matching by App (the UI's actual value) failed: got %q, %v", got, err)
	}
	if _, err := FindMicrophoneApp(sources, "2152"); err == nil {
		t.Fatal("the ephemeral serial must never match: it is not what the UI sends")
	}
	if _, err := FindMicrophoneApp(sources, "unplugged-mic"); err == nil {
		t.Fatal("a microphone no longer present must error")
	}
}
