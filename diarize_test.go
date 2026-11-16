package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestOverlapSpeakerPicksMostTimeInWindow(t *testing.T) {
	spans := []diarizedSpan{
		{StartMS: 0, EndMS: 4000, Speaker: "A"},
		{StartMS: 3000, EndMS: 9000, Speaker: "B"}, // overlaps A by 1s, then alone for 5s
	}
	if got := overlapSpeaker(spans, 0, 9000); got != "B" {
		t.Errorf("got %q, want B (6s vs A's 4s)", got)
	}
	if got := overlapSpeaker(spans, 0, 2000); got != "A" {
		t.Errorf("a window fully inside A's span got %q", got)
	}
	if got := overlapSpeaker(nil, 0, 1000); got != "" {
		t.Errorf("no spans must yield no speaker, got %q", got)
	}
	if got := overlapSpeaker(spans, 20000, 21000); got != "" {
		t.Errorf("a window outside every span must yield no speaker, got %q", got)
	}
}

func TestStableSpeakerKeysOrderedByFirstAppearance(t *testing.T) {
	// Out of chronological order on purpose: the raw labels a diarization call
	// returns are not sorted, and B must still be numbered after A because A
	// spoke first.
	spans := []diarizedSpan{
		{StartMS: 5000, EndMS: 6000, Speaker: "B"},
		{StartMS: 0, EndMS: 1000, Speaker: "A"},
		{StartMS: 2000, EndMS: 3000, Speaker: "A"},
		{StartMS: 7000, EndMS: 8000, Speaker: "C"},
	}
	keys := stableSpeakerKeys(spans)
	if keys["A"] != "spk_0" || keys["B"] != "spk_1" || keys["C"] != "spk_2" {
		t.Errorf("keys = %v, want A=spk_0 B=spk_1 C=spk_2", keys)
	}
}

func TestDiarizePCMAccumulationCapsAndStaysAligned(t *testing.T) {
	run := &meetingRun{}
	run.appendDiarizePCM(make([]byte, 100))
	run.padDiarizeSilence(1000) // 1s of silence at 16kHz*2 bytes = 32000 bytes
	if len(run.diarizePCM) != 100+32000 {
		t.Fatalf("len = %d, want %d", len(run.diarizePCM), 100+32000)
	}
	run.padDiarizeSilence(0) // must be a no-op
	if len(run.diarizePCM) != 100+32000 {
		t.Fatalf("padding 0ms changed the buffer: %d", len(run.diarizePCM))
	}

	capped := &meetingRun{diarizePCM: make([]byte, diarizeMaxBytes-10)}
	capped.appendDiarizePCM(make([]byte, 100)) // only 10 bytes of room left
	if int64(len(capped.diarizePCM)) != diarizeMaxBytes {
		t.Errorf("len = %d, want exactly the cap %d (10 bytes of room, truncated)", len(capped.diarizePCM), diarizeMaxBytes)
	}
	if !capped.diarizeCapped {
		t.Error("exceeding the cap must be recorded")
	}
	beforeSecondCall := len(capped.diarizePCM)
	capped.appendDiarizePCM(make([]byte, 100)) // now fully at the cap: nothing more fits
	if len(capped.diarizePCM) != beforeSecondCall {
		t.Errorf("a full buffer grew further: %d -> %d", beforeSecondCall, len(capped.diarizePCM))
	}
}

func fakeDiarizer(t *testing.T, healthy func() bool, respond func(w http.ResponseWriter, body []byte)) (*diarizer, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if healthy() {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		case "/diarize":
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			respond(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	d := &diarizer{
		baseURL: srv.URL, client: srv.Client(),
		startProcess: func() (*exec.Cmd, error) { return nil, nil },
	}
	return d, srv
}

func TestDiarizerWaitsForHealthThenCalls(t *testing.T) {
	healthyAt := time.Now().Add(30 * time.Millisecond)
	d, _ := fakeDiarizer(t, func() bool { return time.Now().After(healthyAt) }, func(w http.ResponseWriter, body []byte) {
		json.NewEncoder(w).Encode(diarizeResponse{Speakers: []diarizedSpan{{StartMS: 0, EndMS: 1000, Speaker: "A"}}})
	})
	savedPoll := diarizeHealthPoll
	t.Cleanup(func() { diarizeHealthPoll = savedPoll })
	diarizeHealthPoll = 5 * time.Millisecond

	spans, err := d.diarize(context.Background(), []byte("RIFF....WAVEfmt "))
	if err != nil || len(spans) != 1 || spans[0].Speaker != "A" {
		t.Fatalf("spans = %+v, err = %v", spans, err)
	}
	if !d.ready {
		t.Error("a successful call must leave the diarizer marked ready")
	}
}

func TestDiarizerNeverHealthyFailsPermanently(t *testing.T) {
	savedTimeout, savedPoll := diarizeHealthTimeout, diarizeHealthPoll
	t.Cleanup(func() { diarizeHealthTimeout, diarizeHealthPoll = savedTimeout, savedPoll })
	diarizeHealthTimeout, diarizeHealthPoll = 20*time.Millisecond, 5*time.Millisecond

	d, _ := fakeDiarizer(t, func() bool { return false }, nil)
	if _, err := d.diarize(context.Background(), nil); err == nil {
		t.Fatal("a service that never becomes healthy must error")
	}
	if !d.failed {
		t.Error("must be marked failed so later calls do not retry")
	}
	calls := 0
	d.startProcess = func() (*exec.Cmd, error) { calls++; return nil, nil }
	d.diarize(context.Background(), nil)
	if calls != 0 {
		t.Error("a failed diarizer must not try to start the process again")
	}
}

func TestDiarizerPropagatesServiceError(t *testing.T) {
	d, _ := fakeDiarizer(t, func() bool { return true }, func(w http.ResponseWriter, body []byte) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(diarizeResponse{Error: "modelo no cargado"})
	})
	_, err := d.diarize(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "modelo no cargado") {
		t.Fatalf("err = %v, want it to carry the service's message", err)
	}
}

func TestDiarizerStartProcessFailureIsPermanent(t *testing.T) {
	d := &diarizer{baseURL: "http://127.0.0.1:0", client: http.DefaultClient,
		startProcess: func() (*exec.Cmd, error) { return nil, errors.New("python3: not found") }}
	if err := d.ensure(context.Background()); err == nil {
		t.Fatal("a process that cannot start must error")
	}
	if !d.failed {
		t.Error("must be marked failed")
	}
}

// TestRunDiarizationLabelsStoredSegments exercises the whole final pass end to
// end: a fake diarization service, two stored meeting_output segments spoken
// by different (simulated) participants, and the microphone left untouched.
func TestRunDiarizationLabelsStoredSegments(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	micID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackMicrophone, StartMS: 0, EndMS: 2000, Text: "Hola", Status: segmentOK})
	aliceID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 2000, EndMS: 6000, Text: "Buenos días", Status: segmentOK})
	bobID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 6000, EndMS: 10000, Text: "Hola a todos", Status: segmentOK})
	gapID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 10000, EndMS: 12000, Status: segmentGap})

	d, _ := fakeDiarizer(t, func() bool { return true }, func(w http.ResponseWriter, body []byte) {
		json.NewEncoder(w).Encode(diarizeResponse{Speakers: []diarizedSpan{
			{StartMS: 2000, EndMS: 6000, Speaker: "SPEAKER_01"}, // appears second but speaks first
			{StartMS: 6000, EndMS: 10000, Speaker: "SPEAKER_00"},
		}})
	})
	a.diarizer = d
	run.diarizePCM = make([]byte, 1000) // non-empty: there is something to diarize
	t.Setenv("SONARA_DIARIZE_ENABLED", "1")

	var events []MeetingSegmentEvent
	a.emitHook = func(_ string, data any) {
		if event, ok := data.(MeetingSegmentEvent); ok {
			events = append(events, event)
		}
	}

	a.runDiarization(run)

	segments, _ := database.transcriptSegments("m1")
	byID := map[int64]TranscriptSegment{}
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
	if run.diarizePCM != nil {
		t.Error("the accumulated audio must be released once diarization has run")
	}

	names, err := database.speakerNames("m1")
	if err != nil || names["meeting_output:spk_0"] != "Participante 1" || names["meeting_output:spk_1"] != "Participante 2" {
		t.Fatalf("default numbering = %v, err %v", names, err)
	}

	renamed, err := a.RenameMeetingSpeaker("m1", trackOutput, "spk_0", "Ana")
	if err != nil || renamed["meeting_output:spk_0"] != "Ana" || renamed["meeting_output:spk_1"] != "Participante 2" {
		t.Fatalf("renamed = %v, err %v", renamed, err)
	}
}

// TestRunDiarizationSkipsWhenDisabledOrEmpty confirms the two ways this pass
// stays a true no-op: feature off, or nothing was ever accumulated (e.g. the
// meeting_output track never captured anything).
func TestRunDiarizationSkipsWhenDisabledOrEmpty(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: segmentOK})
	calls := 0
	a.diarizer = &diarizer{startProcess: func() (*exec.Cmd, error) { calls++; return nil, nil }}

	run.diarizePCM = []byte{1, 2, 3}
	a.runDiarization(run) // SONARA_DIARIZE_ENABLED not set

	t.Setenv("SONARA_DIARIZE_ENABLED", "1")
	run.diarizePCM = nil
	a.runDiarization(run) // enabled, but nothing was accumulated

	if calls != 0 {
		t.Error("neither case should ever reach the diarization service")
	}
	segments, _ := database.transcriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.SpeakerKey != "" {
			t.Errorf("segment got a speaker_key despite diarization never running: %+v", s)
		}
	}
}

// TestRunDiarizationFailureLeavesSegmentsUntouched confirms a service error
// (model missing, process crashed, etc.) is swallowed: the meeting's text is
// never at risk from this best-effort pass.
func TestRunDiarizationFailureLeavesSegmentsUntouched(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: segmentOK})
	a.diarizer = &diarizer{failed: true, failure: errors.New("no instalado")}
	run.diarizePCM = []byte{1, 2, 3}
	t.Setenv("SONARA_DIARIZE_ENABLED", "1")

	a.runDiarization(run) // must not panic, must not error out to the caller

	segments, _ := database.transcriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.SpeakerKey != "" {
			t.Errorf("a failed diarization pass must not touch segments: %+v", s)
		}
	}
}
