package app

import (
	"context"
	"testing"

	"sonara/internal/storage"
)

func TestSuggestMeetingSpeakerNamesHappyPath(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackMicrophone, StartMS: 0, EndMS: 1000, Text: "Hola Pedro", Status: storage.SegmentOK})
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 1000, EndMS: 2000, Text: "Hola, soy Pedro", Status: storage.SegmentOK, SpeakerKey: "spk_0"})
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 2000, EndMS: 3000, Text: "Hola a todos", Status: storage.SegmentOK, SpeakerKey: "spk_1"})

	var gotTranscript string
	a.suggestNamesHook = func(_ context.Context, transcript string) (map[string]string, error) {
		gotTranscript = transcript
		return map[string]string{
			"spk_0": "Pedro",
			"Yo":    "Impostor", // must never be accepted: not a valid output-track key
			"spk_9": "Fantasma", // must never be accepted: not a key present in this meeting
		}, nil
	}

	got, err := a.SuggestMeetingSpeakerNames("m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["spk_0"] != "Pedro" {
		t.Fatalf("got %v, want only spk_0=Pedro (hallucinated/microphone keys must be dropped)", got)
	}
	if gotTranscript == "" {
		t.Error("the backend must receive the meeting's actual text")
	}
}

func TestSuggestMeetingSpeakerNamesAcceptsTheGenericBucket(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 1000, Text: "soy María", Status: storage.SegmentOK, SpeakerKey: ""})
	a.suggestNamesHook = func(context.Context, string) (map[string]string, error) {
		return map[string]string{"generico": "María"}, nil
	}
	got, err := a.SuggestMeetingSpeakerNames("m1")
	if err != nil || got[""] != "María" {
		t.Fatalf("got %v, err %v, want the generic bucket (\"\") = María", got, err)
	}
}

func TestSuggestMeetingSpeakerNamesNoTextYet(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	a.suggestNamesHook = func(context.Context, string) (map[string]string, error) {
		t.Fatal("must not call the backend when there is nothing to read")
		return nil, nil
	}
	if _, err := a.SuggestMeetingSpeakerNames("m1"); err == nil {
		t.Fatal("a meeting with no spoken text yet must error, not call the backend")
	}
}

func TestSuggestMeetingSpeakerNamesPropagatesFailure(t *testing.T) {
	a, database := testMeetingApp(t)
	testRun(t, a, database)
	database.InsertSegment("m1", storage.TranscriptSegment{Track: storage.TrackOutput, StartMS: 0, EndMS: 1000, Text: "hola", Status: storage.SegmentOK})
	a.suggestNamesHook = func(context.Context, string) (map[string]string, error) {
		return nil, context.DeadlineExceeded
	}
	if _, err := a.SuggestMeetingSpeakerNames("m1"); err == nil {
		t.Fatal("a backend failure must surface, not be swallowed as zero suggestions")
	}
}
