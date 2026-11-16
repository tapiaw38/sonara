package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

// fakeGemini stubs the three HTTP calls diarize() makes: start the resumable
// upload, receive the bytes, and answer the interaction. Good enough to test
// the whole diarizer without a real Gemini account.
func fakeGemini(t *testing.T, spans []diarizedSpan, interactionStatus int, rawOutput string) *diarizer {
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
func fakeGeminiRaw(t *testing.T, interactionStatus int, rawOutput string) *diarizer {
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

	savedBase, savedUpload := geminiBaseURL, geminiUploadURL
	t.Cleanup(func() { geminiBaseURL, geminiUploadURL = savedBase, savedUpload })
	geminiBaseURL, geminiUploadURL = srv.URL+"/v1beta", srv.URL+"/upload/v1beta/files"

	d := &diarizer{client: srv.Client()}
	d.SetAPIKey("test-key")
	return d
}

func TestDiarizeHappyPath(t *testing.T) {
	d := fakeGemini(t, []diarizedSpan{{StartMS: 0, EndMS: 1000, Speaker: "Speaker 1"}}, http.StatusOK, "")
	spans, err := d.diarize(context.Background(), []byte("RIFF....WAVEfmt "))
	if err != nil || len(spans) != 1 || spans[0].Speaker != "Speaker 1" {
		t.Fatalf("spans = %+v, err = %v", spans, err)
	}
}

func TestDiarizeNoKeyConfigured(t *testing.T) {
	d := newDiarizer()
	if d.Configured() {
		t.Fatal("a fresh diarizer must not be configured")
	}
	if _, err := d.diarize(context.Background(), nil); err == nil {
		t.Fatal("calling without a key must error")
	}
}

func TestDiarizePropagatesInteractionFailure(t *testing.T) {
	d := fakeGemini(t, nil, http.StatusInternalServerError, "")
	if _, err := d.diarize(context.Background(), []byte("x")); err == nil {
		t.Fatal("a failing interaction must error")
	}
}

func TestDiarizeRejectsMalformedModelOutput(t *testing.T) {
	d := fakeGemini(t, nil, http.StatusOK, "not a json array")
	if _, err := d.diarize(context.Background(), []byte("x")); err == nil {
		t.Fatal("non-JSON model output must error, not silently produce zero speakers")
	}
}

// TestRunDiarizationLabelsStoredSegments exercises the whole final pass end to
// end: a fake Gemini service, two stored meeting_output segments spoken
// by different (simulated) participants, and the microphone left untouched.
func TestRunDiarizationLabelsStoredSegments(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	micID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackMicrophone, StartMS: 0, EndMS: 2000, Text: "Hola", Status: segmentOK})
	aliceID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 2000, EndMS: 6000, Text: "Buenos días", Status: segmentOK})
	bobID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 6000, EndMS: 10000, Text: "Hola a todos", Status: segmentOK})
	gapID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 10000, EndMS: 12000, Status: segmentGap})

	a.diarizer = fakeGemini(t, []diarizedSpan{
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
	if run.diarizePCM == nil {
		t.Error("runDiarization must not clear the shared buffer itself: runVoiceMatch may still need it")
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

// TestRunDiarizationSkipsWhenNotConfiguredOrEmpty confirms the two ways this
// pass stays a true no-op: no Gemini key set, or nothing was ever
// accumulated (e.g. the meeting_output track never captured anything).
func TestRunDiarizationSkipsWhenNotConfiguredOrEmpty(t *testing.T) {
	a, database := testMeetingApp(t)
	run := testRun(t, a, database)
	segID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: segmentOK})

	run.diarizePCM = []byte{1, 2, 3}
	a.runDiarization(run) // a.diarizer has no key configured

	a.diarizer.SetAPIKey("test-key")
	run.diarizePCM = nil
	a.runDiarization(run) // configured, but nothing was accumulated

	segments, _ := database.transcriptSegments("m1")
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
	segID, _ := database.insertSegment("m1", TranscriptSegment{Track: trackOutput, StartMS: 0, EndMS: 1000, Text: "x", Status: segmentOK})
	a.diarizer = fakeGemini(t, nil, http.StatusInternalServerError, "")
	run.diarizePCM = []byte{1, 2, 3}

	a.runDiarization(run) // must not panic, must not error out to the caller

	segments, _ := database.transcriptSegments("m1")
	for _, s := range segments {
		if s.ID == segID && s.SpeakerKey != "" {
			t.Errorf("a failed diarization pass must not touch segments: %+v", s)
		}
	}
}
