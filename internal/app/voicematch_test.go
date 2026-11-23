package app

import (
	"encoding/json"
	"net/http"
	"testing"

	"sonara/internal/audio"
	"sonara/internal/diarize"
	"sonara/internal/storage"
)

func TestRunVoiceMatchMarksOverlappingSegmentsAsEcho(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	echoID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 2000, Text: "duplicado de mi voz", Status: storage.SegmentOK})
	otherID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 10000, EndMS: 12000, Text: "otra persona", Status: storage.SegmentOK})
	micID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackMicrophone, StartMS: 0, EndMS: 2000, Text: "yo hablando", Status: storage.SegmentOK})

	a.diarizer = fakeGeminiEcho(t, []diarize.EchoSpan{{StartMS: 0, EndMS: 2000}}, http.StatusOK)
	withVoiceSample(t, []byte("muestra"))
	run.diarizePCM = make([]byte, 2*audio.SampleRate*2)

	var events int
	a.emitHook = func(_ string, data any) {
		if _, ok := data.(MeetingSegmentEvent); ok {
			events++
		}
	}

	a.runVoiceMatch(run)

	segments, _ := database.TranscriptSegments("m1")
	byID := map[int64]storage.TranscriptSegment{}
	for _, s := range segments {
		byID[s.ID] = s
	}
	if byID[echoID].Status != storage.SegmentEcho {
		t.Errorf("overlapping segment status = %q, want echo", byID[echoID].Status)
	}
	if byID[echoID].Text != "duplicado de mi voz" {
		t.Error("marking a segment as echo must never erase its stored text")
	}
	if byID[otherID].Status != storage.SegmentOK {
		t.Errorf("a segment outside the echo span must stay ok, got %q", byID[otherID].Status)
	}
	if byID[micID].Status != storage.SegmentOK {
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
	segID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: storage.SegmentOK})

	run.diarizePCM = []byte{1, 2, 3}
	a.runVoiceMatch(run) // no Gemini key, no sample: must not call anything

	a.diarizer = fakeGeminiEcho(t, []diarize.EchoSpan{{StartMS: 0, EndMS: 1000}}, http.StatusOK)
	a.runVoiceMatch(run) // key configured, but no voice sample recorded

	withVoiceSample(t, []byte("muestra"))
	run.diarizePCM = nil
	a.runVoiceMatch(run) // sample + key, but nothing accumulated

	segments, _ := database.TranscriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.Status != storage.SegmentOK {
			t.Errorf("segment got marked despite missing a precondition: %+v", s)
		}
	}
}

func TestRunVoiceMatchFailureLeavesSegmentsUntouched(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: storage.SegmentOK})
	a.diarizer = fakeGeminiEcho(t, nil, http.StatusInternalServerError)
	withVoiceSample(t, []byte("muestra"))
	run.diarizePCM = []byte{1, 2, 3}

	a.runVoiceMatch(run) // must not panic

	segments, _ := database.TranscriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.Status != storage.SegmentOK {
			t.Errorf("a failed echo pass must not touch segments: %+v", s)
		}
	}
}

// fakeGeminiEcho answers the interaction with echo spans instead of speaker
// spans, through the same fake upload endpoints as fakeGemini.
func fakeGeminiEcho(t *testing.T, spans []diarize.EchoSpan, status int) *diarize.Service {
	t.Helper()
	encoded, _ := json.Marshal(spans)
	return fakeGeminiRaw(t, status, string(encoded))
}
