package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// meetBridgeAddr is where the Chrome extension (extension/meet) posts who is
// speaking in Google Meet. Loopback only: nothing off this machine reaches it.
const meetBridgeAddr = "127.0.0.1:47823"

const (
	// meetCaptionLagMS shifts each caption back to when the words were said:
	// Meet shows a caption roughly a second after the speech.
	// ponytail: fixed guess, tune from a real call if names land on the wrong voice.
	meetCaptionLagMS = 1000
	// meetSpanGapMS joins caption updates from one person into a single span
	// when they arrive closer than this.
	meetSpanGapMS = 1500
	// meetMinOverlapMS and meetMinShare: a participant only gets a Meet name
	// with enough evidence, and only when one name clearly dominates.
	meetMinOverlapMS = 2000
	meetMinShare     = 0.6
)

type meetSpan struct {
	name       string
	start, end int64 // ms since the meeting started
}

// meetSpeaking holds the spans for the meeting being recorded.
type meetSpeaking struct {
	mu        sync.Mutex
	sessionID string
	spans     []meetSpan
}

// add records that name was speaking at elapsedMS, extending their last span
// when it is recent enough. It reports whether name is new to this meeting.
func (m *meetSpeaking) add(sessionID, name string, elapsedMS int64) (isNew bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionID != sessionID {
		m.sessionID, m.spans = sessionID, nil
	}
	at := max(elapsedMS-meetCaptionLagMS, 0)
	for i := len(m.spans) - 1; i >= 0; i-- {
		if m.spans[i].name == name {
			if at-m.spans[i].end <= meetSpanGapMS {
				m.spans[i].end = max(m.spans[i].end, at)
			} else {
				m.spans = append(m.spans, meetSpan{name: name, start: at, end: at + 300})
			}
			return false
		}
	}
	m.spans = append(m.spans, meetSpan{name: name, start: at, end: at + 300})
	return true
}

// names lists who Meet showed speaking in this meeting, in order of appearance.
func (m *meetSpeaking) names(sessionID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionID != sessionID {
		return nil
	}
	var out []string
	for _, span := range m.spans {
		if !slices.Contains(out, span.name) {
			out = append(out, span.name)
		}
	}
	return out
}

func (m *meetSpeaking) snapshot(sessionID string) []meetSpan {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionID != sessionID {
		return nil
	}
	return append([]meetSpan(nil), m.spans...)
}

// matchMeetNames picks, for each diarized speaker key of the call's track, the
// Meet name whose speaking spans overlap that speaker's segments the most.
func matchMeetNames(segments []TranscriptSegment, spans []meetSpan) map[string]string {
	overlap := map[string]map[string]int64{}
	for _, segment := range segments {
		if segment.Track != trackOutput || segment.SpeakerKey == "" || segment.Status != segmentOK {
			continue
		}
		for _, span := range spans {
			ms := min(segment.EndMS, span.end) - max(segment.StartMS, span.start)
			if ms <= 0 {
				continue
			}
			if overlap[segment.SpeakerKey] == nil {
				overlap[segment.SpeakerKey] = map[string]int64{}
			}
			overlap[segment.SpeakerKey][span.name] += ms
		}
	}
	names := map[string]string{}
	for key, byName := range overlap {
		var total, best int64
		bestName := ""
		for name, ms := range byName {
			total += ms
			if ms > best {
				best, bestName = ms, name
			}
		}
		if best >= meetMinOverlapMS && float64(best) >= meetMinShare*float64(total) {
			names[key] = bestName
		}
	}
	return names
}

// nameFromMeet turns "Participante 2" into "Participante 2 (Walter)" from what
// the extension saw in Meet. Runs before the LLM naming, which then leaves
// those alone; never touches a name the user typed.
func (a *App) nameFromMeet(sessionID string) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	spans := a.meetSpeaking.snapshot(sessionID)
	if database == nil || len(spans) == 0 {
		return
	}
	segments, err := database.transcriptSegments(sessionID)
	if err != nil {
		return
	}
	labels, err := database.speakerNames(sessionID)
	if err != nil {
		return
	}
	named := 0
	for key, name := range matchMeetNames(segments, spans) {
		label, ok := labels[speakerDisplayKey(trackOutput, key)]
		if !ok || !strings.HasPrefix(label, "Participante") || strings.Contains(label, "(") {
			continue
		}
		if err := database.saveSpeakerName(sessionID, trackOutput, key, label+" ("+name+")"); err != nil {
			logf("meeting %s: could not store the Meet name for %s: %v", sessionID, key, err)
			continue
		}
		named++
	}
	if named > 0 {
		logf("meeting %s: %d participants named from Google Meet", sessionID, named)
		a.emit("meeting:speakers", sessionID)
	}
}

// meetSpeakingHandler receives {"name": "...", "ts": <epoch ms>} from the
// extension's service worker. Only extension origins are accepted, so a web
// page open in the browser cannot inject names.
func (a *App) meetSpeakingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Origin"), "chrome-extension://") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var event struct {
		Name string `json:"name"`
		TS   int64  `json:"ts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&event); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(event.Name)
	if name == "" || len(name) > 80 || strings.ContainsAny(name, "()\n") {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	a.mu.RLock()
	run := a.meeting
	a.mu.RUnlock()
	if run == nil || run.stateNow() != meetingRecording {
		w.WriteHeader(http.StatusConflict) // not recording: nothing to attach it to
		return
	}
	at := time.UnixMilli(event.TS)
	if event.TS == 0 || time.Since(at).Abs() > time.Minute {
		at = time.Now()
	}
	if a.meetSpeaking.add(run.sessionID, name, at.Sub(run.started).Milliseconds()) {
		a.emitMeetingStatus() // the UI shows who Meet reported
	}
	w.WriteHeader(http.StatusNoContent)
}

// startMeetBridge serves the extension until the app exits. A busy port (a
// second Sonara open) only disables the bridge.
func (a *App) startMeetBridge() {
	listener, err := net.Listen("tcp", meetBridgeAddr)
	if err != nil {
		logf("meet bridge disabled: %v", err)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/meet/speaking", a.meetSpeakingHandler)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("meet bridge stopped: %v", err)
		}
	}()
}
