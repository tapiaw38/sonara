package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestIsEcho(t *testing.T) {
	spans := []echoSpan{{StartMS: 0, EndMS: 4000}, {StartMS: 6000, EndMS: 7000}}
	cases := []struct {
		name           string
		startMS, endMS int64
		want           bool
	}{
		{"fully covered", 0, 4000, true},
		{"mostly covered (3/4)", 1000, 5000, true},
		// Window [3000,8000): 1000ms overlaps span 1 (3000-4000), 1000ms
		// overlaps span 2 (6000-7000) = 2000/5000 = 0.4, under the threshold.
		{"under threshold (2/5)", 3000, 8000, false},
		{"no overlap at all", 10000, 11000, false},
		{"zero duration", 1000, 1000, false},
		{"empty spans", 0, 1000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spans := spans
			if c.name == "empty spans" {
				spans = nil
			}
			if got := isEcho(spans, c.startMS, c.endMS); got != c.want {
				t.Errorf("isEcho(%d,%d) = %v, want %v", c.startMS, c.endMS, got, c.want)
			}
		})
	}
}

// fakeGeminiEcho mirrors fakeGemini (diarize_test.go) but answers the
// /v1beta/interactions call with echo spans instead of speaker spans; it
// reuses the exact same fake upload endpoints.
func fakeGeminiEcho(t *testing.T, spans []echoSpan, status int) *diarizer {
	t.Helper()
	encoded, _ := json.Marshal(spans)
	return fakeGeminiRaw(t, status, string(encoded))
}

func TestDetectEchoHappyPath(t *testing.T) {
	d := fakeGeminiEcho(t, []echoSpan{{StartMS: 1000, EndMS: 2000}}, http.StatusOK)
	spans, err := d.detectEcho(context.Background(), []byte("sample"), []byte("meeting"))
	if err != nil || len(spans) != 1 || spans[0].StartMS != 1000 {
		t.Fatalf("spans = %+v, err = %v", spans, err)
	}
}

func TestDetectEchoPropagatesFailure(t *testing.T) {
	d := fakeGeminiEcho(t, nil, http.StatusInternalServerError)
	if _, err := d.detectEcho(context.Background(), []byte("s"), []byte("m")); err == nil {
		t.Fatal("a failing interaction must error")
	}
}

func TestDetectEchoNoKeyConfigured(t *testing.T) {
	d := newDiarizer()
	if _, err := d.detectEcho(context.Background(), nil, nil); err == nil {
		t.Fatal("calling without a key must error")
	}
}

func TestRunVoiceMatchMarksOverlappingSegmentsAsEcho(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	echoID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 0, EndMS: 2000, Text: "duplicado de mi voz", Status: segmentOK})
	otherID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 10000, EndMS: 12000, Text: "otra persona", Status: segmentOK})
	micID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackMicrophone, StartMS: 0, EndMS: 2000, Text: "yo hablando", Status: segmentOK})

	a.diarizer = fakeGeminiEcho(t, []echoSpan{{StartMS: 0, EndMS: 2000}}, http.StatusOK)
	withVoiceSample(t, []byte("muestra"))
	run.diarizePCM = make([]byte, 1000)

	var events int
	a.emitHook = func(_ string, data any) {
		if _, ok := data.(MeetingSegmentEvent); ok {
			events++
		}
	}

	a.runVoiceMatch(run)

	segments, _ := database.transcriptSegments("m1")
	byID := map[int64]TranscriptSegment{}
	for _, s := range segments {
		byID[s.ID] = s
	}
	if byID[echoID].Status != segmentEcho {
		t.Errorf("overlapping segment status = %q, want echo", byID[echoID].Status)
	}
	if byID[echoID].Text != "duplicado de mi voz" {
		t.Error("marking a segment as echo must never erase its stored text")
	}
	if byID[otherID].Status != segmentOK {
		t.Errorf("a segment outside the echo span must stay ok, got %q", byID[otherID].Status)
	}
	if byID[micID].Status != segmentOK {
		t.Error("the microphone track must never be touched by echo detection")
	}
	if events != 1 {
		t.Errorf("expected exactly one meeting:segment event, got %d", events)
	}
	// The shared buffer must survive runVoiceMatch: finishMeeting releases it
	// only once, after both passes, not after each one individually.
	if run.diarizePCM == nil {
		t.Error("runVoiceMatch must not clear the shared diarizePCM buffer itself")
	}
}

func TestRunVoiceMatchSkipsWithoutSampleOrKeyOrAudio(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: segmentOK})

	run.diarizePCM = []byte{1, 2, 3}
	a.runVoiceMatch(run) // no Gemini key, no sample: must not call anything

	a.diarizer = fakeGeminiEcho(t, []echoSpan{{StartMS: 0, EndMS: 1000}}, http.StatusOK)
	a.runVoiceMatch(run) // key configured, but no voice sample recorded

	withVoiceSample(t, []byte("muestra"))
	run.diarizePCM = nil
	a.runVoiceMatch(run) // sample + key, but nothing accumulated

	segments, _ := database.transcriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.Status != segmentOK {
			t.Errorf("segment got marked despite missing a precondition: %+v", s)
		}
	}
}

func TestRunVoiceMatchFailureLeavesSegmentsUntouched(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: segmentOK})
	a.diarizer = fakeGeminiEcho(t, nil, http.StatusInternalServerError)
	withVoiceSample(t, []byte("muestra"))
	run.diarizePCM = []byte{1, 2, 3}

	a.runVoiceMatch(run) // must not panic

	segments, _ := database.transcriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.Status != segmentOK {
			t.Errorf("a failed echo pass must not touch segments: %+v", s)
		}
	}
}
