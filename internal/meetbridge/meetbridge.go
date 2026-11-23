// Package meetbridge receives who is speaking in Google Meet from the Chrome
// extension (extension/meet) and matches those names to diarized speakers.
package meetbridge

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"sonara/internal/logx"
	"sonara/internal/storage"
)

// Addr is where the extension posts. Loopback only: nothing off this machine
// reaches it.
const Addr = "127.0.0.1:47823"

const (
	// captionLagMS shifts each caption back to when the words were said:
	// Meet shows a caption roughly a second after the speech.
	// ponytail: fixed guess, tune from a real call if names land on the wrong voice.
	captionLagMS = 1000
	// spanGapMS joins caption updates from one person into a single span
	// when they arrive closer than this.
	spanGapMS = 1500
	// minOverlapMS and minShare: a participant only gets a Meet name with
	// enough evidence, and only when one name clearly dominates.
	minOverlapMS = 2000
	minShare     = 0.6
	maxNames     = 50
	maxSpans     = 5000
)

// Span is one stretch of time Meet showed name speaking.
type Span struct {
	Name       string
	Start, End int64 // ms since the meeting started
}

// Tracker holds the spans for the meeting being recorded. The zero value is
// ready to use and safe for concurrent callers.
type Tracker struct {
	mu        sync.Mutex
	sessionID string
	spans     []Span
	names     []string
}

// Add records that name was speaking at elapsedMS, extending their last span
// when it is recent enough. It reports whether name is new to this meeting.
func (t *Tracker) Add(sessionID, name string, elapsedMS int64) (isNew bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionID != sessionID {
		t.sessionID, t.spans, t.names = sessionID, nil, nil
	}
	at := max(elapsedMS-captionLagMS, 0)
	for i := len(t.spans) - 1; i >= 0; i-- {
		if t.spans[i].Name == name {
			if at-t.spans[i].End <= spanGapMS {
				t.spans[i].End = max(t.spans[i].End, at)
			} else if len(t.spans) < maxSpans { // bounded: a runaway sender cannot grow it forever
				t.spans = append(t.spans, Span{Name: name, Start: at, End: at + 300})
			}
			return false
		}
	}
	if len(t.names) >= maxNames || len(t.spans) >= maxSpans {
		return false
	}
	t.names = append(t.names, name)
	t.spans = append(t.spans, Span{Name: name, Start: at, End: at + 300})
	return true
}

// Names lists who Meet showed speaking in this meeting, in order of appearance.
func (t *Tracker) Names(sessionID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionID != sessionID {
		return nil
	}
	return slices.Clone(t.names)
}

// Snapshot copies this meeting's spans.
func (t *Tracker) Snapshot(sessionID string) []Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionID != sessionID {
		return nil
	}
	return append([]Span(nil), t.spans...)
}

// MatchNames picks, for each diarized speaker key of the call's track, the
// Meet name whose speaking spans overlap that speaker's segments the most.
func MatchNames(segments []storage.TranscriptSegment, spans []Span) map[string]string {
	overlap := map[string]map[string]int64{}
	for _, segment := range segments {
		if segment.Track != storage.TrackOutput || segment.SpeakerKey == "" || segment.Status != storage.SegmentOK {
			continue
		}
		for _, span := range spans {
			ms := min(segment.EndMS, span.End) - max(segment.StartMS, span.Start)
			if ms <= 0 {
				continue
			}
			if overlap[segment.SpeakerKey] == nil {
				overlap[segment.SpeakerKey] = map[string]int64{}
			}
			overlap[segment.SpeakerKey][span.Name] += ms
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
		if best >= minOverlapMS && float64(best) >= minShare*float64(total) {
			names[key] = bestName
		}
	}
	return names
}

// Recorder is what the bridge needs from the app: the meeting being recorded,
// if any. Defined here, by its consumer, so the app depends on nothing new.
type Recorder interface {
	ActiveRecording() (sessionID string, started time.Time, ok bool)
}

// Handler receives {"name": "...", "ts": <epoch ms>} from the extension's
// service worker into tracker. SONARA_MEET_EXTENSION_ID pins the origin when
// set. Otherwise, any extension origin works because unpacked IDs depend on
// the install path. Events dropped at the tracker's limits still get 204.
// onNewName runs the first time a name is stored in a meeting.
func Handler(recorder Recorder, tracker *Tracker, onNewName func()) http.HandlerFunc {
	const extensionOrigin = "chrome-extension://"
	extensionID := os.Getenv("SONARA_MEET_EXTENSION_ID")
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if r.Method != http.MethodPost || !strings.HasPrefix(origin, extensionOrigin) ||
			(extensionID != "" && origin != extensionOrigin+extensionID) {
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
		sessionID, started, ok := recorder.ActiveRecording()
		if !ok {
			w.WriteHeader(http.StatusConflict) // not recording: nothing to attach it to
			return
		}
		at := time.UnixMilli(event.TS)
		if event.TS == 0 || time.Since(at).Abs() > time.Minute {
			at = time.Now()
		}
		if tracker.Add(sessionID, name, at.Sub(started).Milliseconds()) && onNewName != nil {
			onNewName()
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Start serves handler on Addr until the process exits. A busy port (a
// second Sonara open) only disables the bridge.
func Start(handler http.Handler) {
	listener, err := net.Listen("tcp", Addr)
	if err != nil {
		logx.Logf("meet bridge disabled: %v", err)
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/meet/speaking", handler)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logx.Logf("meet bridge stopped: %v", err)
		}
	}()
}
