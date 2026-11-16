package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func availableAudioSources(ctx context.Context) ([]AudioSource, error) {
	output, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return nil, err
	}
	return parseAudioSources(output)
}

func availableMicrophones(ctx context.Context) ([]AudioSource, error) {
	output, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return nil, err
	}
	return parseMicrophones(output)
}

// Microphones lists capture devices (PipeWire Audio/Source nodes), kept apart
// from AudioSources so the UI can never offer an output stream as a microphone.
func (a *App) Microphones() ([]AudioSource, error) {
	return availableMicrophones(a.requestContext())
}

func (a *App) AudioSources() ([]AudioSource, error) {
	return availableAudioSources(a.requestContext())
}

func (a *App) SetAudioSource(target string) (string, error) {
	if target != "" {
		sources, err := a.AudioSources()
		if err != nil {
			return "", err
		}
		app := ""
		for _, source := range sources {
			if source.Target == target {
				app = source.App
				break
			}
		}
		if app == "" {
			return "", fmt.Errorf("fuente de audio ya no disponible")
		}
		// Remembering the app, not just the node, is what lets the choice
		// survive the meeting app swapping streams mid-call and the app restarting.
		// An explicit pick is never a guess, and clearing the flag here means the
		// warning disappears the moment the user acts instead of on the next poll.
		a.mu.Lock()
		a.selectedAudioSource, a.preferredAudioApp = target, app
		a.audioGuessing = false
		database := a.store
		a.mu.Unlock()
		if database != nil {
			if err := database.setSetting(settingAudioApp, app); err != nil {
				logf("could not persist the preferred audio application: %v", err)
			}
		}
		return target, nil
	}
	a.mu.Lock()
	a.selectedAudioSource, a.preferredAudioApp = "", ""
	database := a.store
	a.mu.Unlock()
	if database != nil {
		if err := database.setSetting(settingAudioApp, ""); err != nil {
			logf("could not clear the preferred audio application: %v", err)
		}
	}
	return "", nil
}

// audioSourceState is what the UI needs to warn the user a guess was made:
// whether the current target came from a confident match, and how many
// candidate streams existed when it was picked.
type audioSourceState struct {
	target     string
	guessed    bool
	candidates int
}

func (a *App) audioTarget(ctx context.Context) (string, error) {
	state, err := a.resolveAudioTarget(ctx)
	a.mu.Lock()
	a.audioGuessing, a.audioCandidates = state.guessed, state.candidates
	a.mu.Unlock()
	return state.target, err
}

func (a *App) resolveAudioTarget(ctx context.Context) (audioSourceState, error) {
	sources, err := availableAudioSources(ctx)
	if err != nil {
		return audioSourceState{}, err
	}
	candidates := len(sources)
	a.mu.RLock()
	selected, preferred := a.selectedAudioSource, a.preferredAudioApp
	a.mu.RUnlock()
	if selected != "" {
		for _, source := range sources {
			if source.Target == selected {
				return audioSourceState{target: selected, candidates: candidates}, nil
			}
		}
		// The node is gone, which happens whenever the meeting app opens a new
		// stream. Re-latch onto the same application rather than dying mid-call
		// and demanding the user pick again.
		if adopted, err := adoptPreferredApp(sources, preferred); err == nil {
			a.mu.Lock()
			a.selectedAudioSource = adopted
			a.mu.Unlock()
			logDedup("audio-latch", "audio source %s vanished; re-latched onto %q as target %s", selected, preferred, adopted)
			return audioSourceState{target: adopted, candidates: candidates}, nil
		}
		// The preferred application is gone or ambiguous — Teams Web is a
		// Chrome tab, so "google chrome chrome" matches every other tab too,
		// and this is the common case, not the exception. Falling through to
		// the best-guess heuristic below beats going deaf for the rest of the
		// call: a wrong guess shows up as nonsense in the transcript and gets
		// corrected by hand; silence does not announce itself at all.
		if guess, guessed, err := chooseAudioSource(sources); err == nil {
			a.mu.Lock()
			a.selectedAudioSource = guess
			a.mu.Unlock()
			logDedup("audio-latch", "audio source %s vanished; %q is ambiguous, guessing %s", selected, preferred, guess)
			return audioSourceState{target: guess, guessed: guessed, candidates: candidates}, nil
		}
		logDedup("audio-latch", "audio source %s vanished and %q is ambiguous or absent; waiting for the user", selected, preferred)
		return audioSourceState{candidates: candidates}, fmt.Errorf("fuente elegida desapareció; actualizá la lista")
	}
	// Nothing was picked in this run, but a previous one remembered which
	// application carries the meeting. If that application owns exactly one
	// stream the choice needs no user action at all.
	if adopted, err := adoptPreferredApp(sources, preferred); err == nil {
		a.mu.Lock()
		a.selectedAudioSource = adopted
		a.mu.Unlock()
		logDedup("audio-latch", "using remembered application %q as target %s", preferred, adopted)
		return audioSourceState{target: adopted, candidates: candidates}, nil
	}
	target, guessed, err := chooseAudioSource(sources)
	if err != nil {
		return audioSourceState{candidates: candidates}, err
	}
	return audioSourceState{target: target, guessed: guessed, candidates: candidates}, nil
}

// adoptPreferredApp finds the replacement stream for an application the user
// already chose. It only commits when the app owns exactly one stream, so this
// can never silently swap to the wrong tab.
func adoptPreferredApp(sources []AudioSource, preferred string) (string, error) {
	if preferred == "" {
		return "", fmt.Errorf("no preferred application recorded")
	}
	match := ""
	for _, source := range sources {
		if source.App != preferred {
			continue
		}
		if match != "" {
			return "", fmt.Errorf("application %q owns several streams", preferred)
		}
		match = source.Target
	}
	if match == "" {
		return "", fmt.Errorf("application %q has no stream", preferred)
	}
	return match, nil
}

// serialTarget is the pw-record target for a node: its object.serial, falling
// back to the node name.
func serialTarget(props map[string]any) string {
	switch value := props["object.serial"].(type) {
	case float64:
		if value > 0 {
			return strconv.FormatInt(int64(value), 10)
		}
	case string:
		if _, err := strconv.ParseUint(value, 10, 64); err == nil {
			return value
		}
	}
	name, _ := props["node.name"].(string)
	return name
}

// parseMicrophones keeps real capture devices only. Monitors of output sinks
// are not Audio/Source nodes, so system sounds and notifications never appear
// as a microphone. App carries the stable node name used to remember the pick.
func parseMicrophones(output []byte) ([]AudioSource, error) {
	var nodes []struct {
		Type string `json:"type"`
		Info struct {
			State string         `json:"state"`
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(output, &nodes); err != nil {
		return nil, err
	}
	sources := make([]AudioSource, 0)
	for _, node := range nodes {
		props := node.Info.Props
		if node.Type != "PipeWire:Interface:Node" || props["media.class"] != "Audio/Source" {
			continue
		}
		name, _ := props["node.name"].(string)
		target := serialTarget(props)
		if name == "" || target == "" {
			continue
		}
		label, _ := props["node.description"].(string)
		if strings.TrimSpace(label) == "" {
			label = name
		}
		sources = append(sources, AudioSource{
			Target: target, Label: label, Running: node.Info.State == "running",
			App: name, Kind: kindInput,
		})
	}
	return sources, nil
}

func parseAudioSources(output []byte) ([]AudioSource, error) {
	var nodes []struct {
		Type string `json:"type"`
		Info struct {
			State string         `json:"state"`
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(output, &nodes); err != nil {
		return nil, err
	}
	sources := make([]AudioSource, 0)
	for _, node := range nodes {
		props := node.Info.Props
		if node.Type != "PipeWire:Interface:Node" || props["media.class"] != "Stream/Output/Audio" {
			continue
		}
		identity := strings.ToLower(fmt.Sprintf("%v %v %v", props["application.name"], props["application.process.binary"], props["node.name"]))
		if !isMeetingAudioIdentity(identity) {
			continue
		}
		target := serialTarget(props)
		if target == "" {
			continue
		}
		appName, _ := props["application.name"].(string)
		binaryName, _ := props["application.process.binary"].(string)
		mediaName, _ := props["media.name"].(string)
		label := strings.TrimSpace(appName)
		if label == "" {
			label = "Audio de reunión"
		}
		if mediaName != "" {
			label += " · " + mediaName
		}
		sources = append(sources, AudioSource{
			Target: target, Label: label, Running: node.Info.State == "running",
			Meeting: strings.Contains(strings.ToLower(appName+" "+binaryName), "teams"),
			App:     strings.ToLower(strings.TrimSpace(appName + " " + binaryName)),
			Kind:    kindOutput,
		})
	}
	return sources, nil
}

// chooseAudioSource picks a target among several candidates with no selection
// recorded. guessed reports whether the pick is a heuristic rather than a
// confident, nameable match — the UI uses it to tell the user it might be
// listening to the wrong tab.
func chooseAudioSource(sources []AudioSource) (target string, guessed bool, err error) {
	if len(sources) == 0 {
		return "", false, fmt.Errorf("no hay stream de salida de Teams o navegador")
	}
	if len(sources) == 1 {
		return sources[0].Target, false, nil
	}
	var activeMeeting string
	for _, source := range sources {
		if source.Running && source.Meeting {
			if activeMeeting != "" {
				return "", false, fmt.Errorf("varias reuniones activas; elegí la fuente")
			}
			activeMeeting = source.Target
		}
	}
	if activeMeeting != "" {
		return activeMeeting, false, nil
	}
	// Teams Web is a Chrome tab: its process name is "chrome", the same as every
	// other tab, so Meeting never fires for it and this is the ordinary case,
	// not an edge case. With no name to go on, guess the stream most likely to
	// be the live call — running, and the most recently opened among those —
	// rather than staying deaf until someone opens the picker mid-interview. A
	// wrong guess is visible garbage in the transcript; silence is invisible.
	target, err = newestRunningSource(sources)
	return target, err == nil, err
}

// newestRunningSource guesses the live call among several candidates with no
// name to tell them apart. Running beats idle, and within the same state the
// highest serial is the stream a call most recently opened — but running is a
// preference, not a requirement: a call that just went quiet for a second is a
// better guess than returning no guess at all.
func newestRunningSource(sources []AudioSource) (string, error) {
	best, bestRank, bestSerial := "", -1, int64(-1)
	for _, source := range sources {
		serial, err := strconv.ParseInt(source.Target, 10, 64)
		if err != nil {
			continue
		}
		rank := 0
		if source.Running {
			rank = 1
		}
		if rank > bestRank || (rank == bestRank && serial > bestSerial) {
			best, bestRank, bestSerial = source.Target, rank, serial
		}
	}
	if best == "" {
		return "", fmt.Errorf("varias fuentes posibles; elegí la de la reunión")
	}
	return best, nil
}

func pickChromeTarget(output []byte) (string, error) {
	sources, err := parseAudioSources(output)
	if err != nil {
		return "", err
	}
	target, _, err := chooseAudioSource(sources)
	return target, err
}

func isMeetingAudioIdentity(identity string) bool {
	for _, marker := range []string{"chrome", "chromium", "google-chrome", "msedge", "microsoft edge", "firefox", "teams", "ms-teams"} {
		if strings.Contains(identity, marker) {
			return true
		}
	}
	return false
}
