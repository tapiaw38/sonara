package main

import "testing"

// A meeting app replaces its output stream when a call starts, reconnects or
// renegotiates, so the chosen serial disappears while the call is still up.
// Re-latching by application is what keeps capture alive through that.
func TestAdoptPreferredApp(t *testing.T) {
	chrome := AudioSource{Target: "501", App: "google chrome chrome"}
	firefox := AudioSource{Target: "502", App: "firefox firefox"}
	otherChrome := AudioSource{Target: "503", App: "google chrome chrome"}

	got, err := adoptPreferredApp([]AudioSource{firefox, chrome}, "google chrome chrome")
	if err != nil || got != "501" {
		t.Errorf("unique match = (%q, %v), want 501", got, err)
	}

	// Two streams from the same app could be the call or another tab. Guessing
	// wrong means transcribing the wrong audio, so it must refuse.
	if _, err := adoptPreferredApp([]AudioSource{chrome, otherChrome}, "google chrome chrome"); err == nil {
		t.Error("ambiguous application must not be adopted")
	}

	if _, err := adoptPreferredApp([]AudioSource{firefox}, "google chrome chrome"); err == nil {
		t.Error("absent application must not be adopted")
	}

	// Nothing was ever chosen explicitly, so there is nothing to re-latch onto.
	if _, err := adoptPreferredApp([]AudioSource{chrome}, ""); err == nil {
		t.Error("empty preference must not be adopted")
	}
}

// Teams Web runs inside Chrome, so its process name is "Google Chrome" like
// any other tab: Meeting, which only fires on the literal name "teams", never
// fires for it. A real incident went silent for 12 minutes in exactly this
// situation — several Chrome streams, none of them named — because
// chooseAudioSource had no fallback once the Meeting shortcut came up empty.
func TestChooseAudioSourceGuessesAmongUnnamedChromeStreams(t *testing.T) {
	older := AudioSource{Target: "100", App: "google chrome chrome", Running: true}
	newer := AudioSource{Target: "200", App: "google chrome chrome", Running: true}
	idleNewest := AudioSource{Target: "300", App: "google chrome chrome", Running: false}

	got, guessed, err := chooseAudioSource([]AudioSource{older, newer})
	if err != nil || got != "200" || !guessed {
		t.Errorf("two unnamed Chrome streams = (%q, %v, %v), want (200, guessed, nil)", got, guessed, err)
	}

	// Idle must lose to running even when it is the more recently opened
	// stream: a call that is actually live outranks a tab that merely exists.
	got, guessed, err = chooseAudioSource([]AudioSource{older, newer, idleNewest})
	if err != nil || got != "200" || !guessed {
		t.Errorf("with an idle newer stream = (%q, %v, %v), want (200, guessed, nil)", got, guessed, err)
	}

	// A literally named Teams stream still wins outright over the guess, and
	// is reported as confident rather than guessed.
	teams := AudioSource{Target: "400", App: "microsoft teams teams", Running: true, Meeting: true}
	got, guessed, err = chooseAudioSource([]AudioSource{older, newer, teams})
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
	named := AudioSource{Target: "pulse-sink-name"} // no object.serial, fell back to a name
	if _, err := newestRunningSource([]AudioSource{named}); err == nil {
		t.Error("a non-numeric target must not be guessable")
	}
}
