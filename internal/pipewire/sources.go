// Package pipewire talks to the PipeWire tools (pw-dump, pw-cli, pipewire):
// which streams and microphones exist, and the echo canceller.
package pipewire

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Source kinds: what an application plays, and what records the user.
const (
	KindOutput = "output"
	KindInput  = "input"
)

// Source is one PipeWire node Sonara can record: a playing stream or a microphone.
type Source struct {
	Target  string `json:"target"`
	Label   string `json:"label"`
	Running bool   `json:"running"`
	Meeting bool   `json:"meeting"`
	// App is the owning application, which survives the stream itself: a serial
	// identifies one node, and meeting apps replace nodes mid-call.
	App string `json:"app"`
	// Kind tells the UI which list this belongs to; an output stream must never
	// be offered as a microphone.
	Kind string `json:"kind"`
}

// OutputStreams lists the audio streams applications are playing.
func OutputStreams(ctx context.Context) ([]Source, error) {
	output, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return nil, err
	}
	return parseAudioSources(output)
}

// ListMicrophones lists the capture devices, excluding monitors and virtual nodes.
func ListMicrophones(ctx context.Context) ([]Source, error) {
	output, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return nil, err
	}
	return parseMicrophones(output)
}

// AdoptPreferredApp returns the one stream owned by the remembered application,
// and fails when there is none or more than one.
func AdoptPreferredApp(sources []Source, preferred string) (string, error) {
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

func parseMicrophones(output []byte) ([]Source, error) {
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
	sources := make([]Source, 0)
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
		sources = append(sources, Source{
			Target: target, Label: label, Running: node.Info.State == "running",
			App: name, Kind: KindInput,
		})
	}
	return sources, nil
}

func parseAudioSources(output []byte) ([]Source, error) {
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
	sources := make([]Source, 0)
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
		sources = append(sources, Source{
			Target: target, Label: label, Running: node.Info.State == "running",
			Meeting: strings.Contains(strings.ToLower(appName+" "+binaryName), "teams"),
			App:     strings.ToLower(strings.TrimSpace(appName + " " + binaryName)),
			Kind:    KindOutput,
		})
	}
	return sources, nil
}

// ChooseStream picks the meeting stream to record. guessed reports that
// several candidates fit and the pick is a heuristic the UI should flag.
func ChooseStream(sources []Source) (target string, guessed bool, err error) {
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

	target, err = newestRunningSource(sources)
	return target, err == nil, err
}

func newestRunningSource(sources []Source) (string, error) {
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
	target, _, err := ChooseStream(sources)
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

// FindMicrophoneApp looks up target (the stable node name, AudioSource.App)
// among the microphones PipeWire currently reports. target is never the
// ephemeral AudioSource.Target serial — that gets recreated on every boot or
// reconnect, so a match on it would never stick; the UI's <option> sends App
// for exactly this reason.
func FindMicrophoneApp(sources []Source, target string) (string, error) {
	for _, source := range sources {
		if source.App == target {
			return source.App, nil
		}
	}
	return "", fmt.Errorf("el micrófono ya no está disponible")
}
