package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sonara/internal/diarize"
	"sonara/internal/storage"
)

func TestDiarizePCMAccumulationCapsAndStaysAligned(t *testing.T) {
	run := &meetingRun{}
	run.appendDiarizePCM(storage.TrackOutput, make([]byte, 100))
	run.padDiarizeSilence(storage.TrackOutput, 1000) // 1s of silence at 16kHz*2 bytes = 32000 bytes
	if len(run.diarizePCM) != 100+32000 {
		t.Fatalf("len = %d, want %d", len(run.diarizePCM), 100+32000)
	}
	run.padDiarizeSilence(storage.TrackOutput, 0) // must be a no-op
	if len(run.diarizePCM) != 100+32000 {
		t.Fatalf("padding 0ms changed the buffer: %d", len(run.diarizePCM))
	}

	capped := &meetingRun{diarizePCM: make([]byte, diarizeMaxBytes-10)}
	capped.appendDiarizePCM(storage.TrackOutput, make([]byte, 100)) // only 10 bytes of room left
	if int64(len(capped.diarizePCM)) != diarizeMaxBytes {
		t.Errorf("len = %d, want exactly the cap %d (10 bytes of room, truncated)", len(capped.diarizePCM), diarizeMaxBytes)
	}
	if !capped.diarizeCapped {
		t.Error("exceeding the cap must be recorded")
	}
	beforeSecondCall := len(capped.diarizePCM)
	capped.appendDiarizePCM(storage.TrackOutput, make([]byte, 100)) // now fully at the cap: nothing more fits
	if len(capped.diarizePCM) != beforeSecondCall {
		t.Errorf("a full buffer grew further: %d -> %d", beforeSecondCall, len(capped.diarizePCM))
	}
}

// fakeGemini stubs the three HTTP calls diarize() makes: start the resumable
// upload, receive the bytes, and answer the interaction. Good enough to test
// the whole diarizer without a real Gemini account.
func fakeGemini(t *testing.T, spans []diarize.Span, interactionStatus int, rawOutput string) *diarize.Service {
	t.Helper()
	if rawOutput == "" {
		encoded, _ := json.Marshal(spans)
		rawOutput = string(encoded)
	}
	return fakeGeminiRaw(t, interactionStatus, rawOutput)
}

// fakeGeminiRaw is the shared fake behind both fakeGemini (diarization) and
// fakeGeminiEcho (voicematch_test.go): the three HTTP calls diarize()/
// detectEcho() make — start the resumable upload, receive the bytes, and
// answer the interaction with whatever JSON text the caller wants echoed
// back as the model's output.
func fakeGeminiRaw(t *testing.T, interactionStatus int, rawOutput string) *diarize.Service {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/v1beta/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-goog-upload-url", "http://"+r.Host+"/upload/bytes")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/upload/bytes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"file": map[string]string{"uri": "https://files/abc", "name": "files/abc"}})
	})
	mux.HandleFunc("/v1beta/interactions", func(w http.ResponseWriter, r *http.Request) {
		if interactionStatus != http.StatusOK {
			w.WriteHeader(interactionStatus)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"steps": []map[string]any{
				{"type": "model_output", "content": []map[string]string{{"type": "text", "text": rawOutput}}},
			},
		})
	})
	mux.HandleFunc("/v1beta/files/abc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	d := diarize.NewService(diarize.NewGemini(srv.Client(), srv.URL+"/v1beta", srv.URL+"/upload/v1beta/files"))
	d.SetAPIKey("test-key")
	return d
}

// TestRunDiarizationLabelsStoredSegments exercises the whole final pass end to
// end: a fake Gemini service, two stored meeting_output segments spoken
// by different (simulated) participants, and the microphone left untouched.
func TestRunDiarizationLabelsStoredSegments(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	micID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackMicrophone, StartMS: 0, EndMS: 2000, Text: "Hola", Status: storage.SegmentOK})
	aliceID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 2000, EndMS: 6000, Text: "Buenos días", Status: storage.SegmentOK})
	bobID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 6000, EndMS: 10000, Text: "Hola a todos", Status: storage.SegmentOK})
	gapID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 10000, EndMS: 12000, Status: storage.SegmentGap})

	a.diarizer = fakeGemini(t, []diarize.Span{
		{StartMS: 2000, EndMS: 6000, Speaker: "Speaker 2"}, // appears second but speaks first
		{StartMS: 6000, EndMS: 10000, Speaker: "Speaker 1"},
	}, http.StatusOK, "")
	run.diarizePCM = make([]byte, 1000) // non-empty: there is something to diarize

	var events []MeetingSegmentEvent
	a.emitHook = func(_ string, data any) {
		if event, ok := data.(MeetingSegmentEvent); ok {
			events = append(events, event)
		}
	}

	a.runDiarization(run)

	segments, _ := database.TranscriptSegments("m1")
	byID := map[int64]storage.TranscriptSegment{}
	for _, s := range segments {
		byID[s.ID] = s
	}
	if byID[micID].SpeakerKey != "" {
		t.Errorf("the microphone must never get a speaker_key, got %q", byID[micID].SpeakerKey)
	}
	if byID[aliceID].SpeakerKey != "spk_0" {
		t.Errorf("first speaker (by time) = %q, want spk_0", byID[aliceID].SpeakerKey)
	}
	if byID[bobID].SpeakerKey != "spk_1" {
		t.Errorf("second speaker = %q, want spk_1", byID[bobID].SpeakerKey)
	}
	if byID[gapID].SpeakerKey != "" {
		t.Errorf("a gap row must stay untouched, got %q", byID[gapID].SpeakerKey)
	}
	if len(events) != 2 {
		t.Errorf("expected one meeting:segment event per labeled row, got %d", len(events))
	}
	if run.diarizePCM == nil {
		t.Error("runDiarization must not clear the shared buffer itself: runVoiceMatch may still need it")
	}

	names, err := database.SpeakerNames("m1")
	if err != nil || names["meeting_output:spk_0"] != "Participante 1" || names["meeting_output:spk_1"] != "Participante 2" {
		t.Fatalf("default numbering = %v, err %v", names, err)
	}

	renamed, err := a.RenameMeetingSpeaker("m1", storage.TrackOutput, "spk_0", "Ana")
	if err != nil || renamed["meeting_output:spk_0"] != "Ana" || renamed["meeting_output:spk_1"] != "Participante 2" {
		t.Fatalf("renamed = %v, err %v", renamed, err)
	}
}

// TestRunDiarizationSkipsWhenNotConfiguredOrEmpty confirms the two ways this
// pass stays a true no-op: no Gemini key set, or nothing was ever
// accumulated (e.g. the meeting_output track never captured anything).
func TestRunDiarizationSkipsWhenNotConfiguredOrEmpty(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: storage.SegmentOK})

	run.diarizePCM = []byte{1, 2, 3}
	a.runDiarization(run) // a.diarizer has no key configured

	a.diarizer.SetAPIKey("test-key")
	run.diarizePCM = nil
	a.runDiarization(run) // configured, but nothing was accumulated

	segments, _ := database.TranscriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.SpeakerKey != "" {
			t.Errorf("segment got a speaker_key despite diarization never running: %+v", s)
		}
	}
}

// TestRunDiarizationFailureLeavesSegmentsUntouched confirms a service error
// (bad key, network, Gemini outage) is swallowed: the meeting's text is never
// at risk from this best-effort pass.
func TestRunDiarizationFailureLeavesSegmentsUntouched(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: storage.SegmentOK})
	a.diarizer = fakeGemini(t, nil, http.StatusInternalServerError, "")
	run.diarizePCM = []byte{1, 2, 3}

	a.runDiarization(run) // must not panic, must not error out to the caller

	segments, _ := database.TranscriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.SpeakerKey != "" {
			t.Errorf("a failed diarization pass must not touch segments: %+v", s)
		}
	}
}

// The UI must be able to say why everyone is still "Participante": a failure
// and a success both leave a note tied to that meeting.
func TestRunDiarizationLeavesANoteForTheUI(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 4000, Text: "hola", Status: storage.SegmentOK})

	a.diarizer = fakeGemini(t, nil, http.StatusInternalServerError, "")
	run.diarizePCM = []byte{1, 2, 3}
	a.runDiarization(run)
	status := a.MeetingTranscriptStatus()
	if status.DiarizeSessionID != "m1" || !strings.Contains(status.DiarizeNote, "No se pudieron separar") {
		t.Fatalf("failure note = %q (session %q)", status.DiarizeNote, status.DiarizeSessionID)
	}

	a.diarizer = fakeGemini(t, []diarize.Span{{StartMS: 0, EndMS: 4000, Speaker: "A"}}, http.StatusOK, "")
	a.diarizer.CloudPeriod = 20 * time.Millisecond
	run.diarizePCM = []byte{1, 2, 3}
	a.runDiarization(run)
	if note := a.MeetingTranscriptStatus().DiarizeNote; note != "Participantes detectados: 1." {
		t.Fatalf("success note = %q", note)
	}
}

// While the meeting is still recording, participants get labeled without
// waiting for it to end, and a pass that changes nothing announces nothing.
func TestPeriodicDiarizationLabelsDuringTheMeeting(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	id, _ := database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 4000, Text: "hola", Status: storage.SegmentOK})
	a.diarizer = fakeGemini(t, []diarize.Span{{StartMS: 0, EndMS: 4000, Speaker: "A"}}, http.StatusOK, "")
	a.diarizer.CloudPeriod = 20 * time.Millisecond

	var mu sync.Mutex
	events := 0
	a.emitHook = func(_ string, data any) {
		if _, ok := data.(MeetingSegmentEvent); ok {
			mu.Lock()
			events++
			mu.Unlock()
		}
	}
	run.diarizePCM = make([]byte, diarize.CloudMinGrowth+1) // a minute of audio recorded so far

	ctx, cancel := context.WithCancel(context.Background())
	run.periodic.Add(1)
	go a.periodicDiarization(ctx, run)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		segments, _ := database.TranscriptSegments("m1")
		if len(segments) == 1 && segments[0].ID == id && segments[0].SpeakerKey == "spk_0" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // several more periods with no new audio
	cancel()
	run.periodic.Wait()

	segments, _ := database.TranscriptSegments("m1")
	if segments[0].SpeakerKey != "spk_0" {
		t.Fatalf("speaker_key = %q: the segment should be labeled before the meeting ends", segments[0].SpeakerKey)
	}
	mu.Lock()
	defer mu.Unlock()
	if events != 1 {
		t.Errorf("%d events: only the first pass changes anything, and unchanged labels must not be re-announced; with no new audio there are no new passes", events)
	}
	if note := a.MeetingTranscriptStatus().DiarizeNote; !strings.Contains(note, "hasta ahora") {
		t.Errorf("a mid-meeting note should say it is partial, got %q", note)
	}
}
