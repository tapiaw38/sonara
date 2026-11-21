package main

import "testing"

func TestParseSherpaSpans(t *testing.T) {
	output := "progress 100.00%\nStarted\n1.583 -- 3.406 speaker_00\n9.346 -- 11.472 speaker_01\nDuration : 16.000 s\n"
	spans := parseSherpaSpans(output)
	if len(spans) != 2 || spans[0] != (diarizedSpan{StartMS: 1583, EndMS: 3406, Speaker: "speaker_00"}) ||
		spans[1].Speaker != "speaker_01" || spans[1].StartMS != 9346 {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestParticipantLabel(t *testing.T) {
	if participantLabel(trackMicrophone, 1) != "Participante B" || participantLabel(trackOutput, 1) != "Participante 2" {
		t.Fatal("microphone people are lettered, call people numbered")
	}
}

func TestMergeMinorSpeakers(t *testing.T) {
	spans := []diarizedSpan{
		{StartMS: 0, EndMS: 10000, Speaker: "a"},
		{StartMS: 10000, EndMS: 10800, Speaker: "ghost"}, // 0.8s: not a person
		{StartMS: 11000, EndMS: 20000, Speaker: "b"},
		{StartMS: 19500, EndMS: 30000, Speaker: "b"},
	}
	got := mergeMinorSpeakers(spans)
	seen := map[string]bool{}
	for _, span := range got {
		seen[span.Speaker] = true
	}
	if len(seen) != 2 || seen["ghost"] || got[1].Speaker != "a" {
		t.Fatalf("got %+v; want ghost merged into its nearest real speaker", got)
	}
}

func TestMemoryRoleInPractice(t *testing.T) {
	if memoryRole("practice", "user") != "me (what I said out loud)" || memoryRole("practice", "assistant") != "interviewer" ||
		memoryRole("interview", "user") != "user" || memoryRole("interview", "me") != "me (what I said out loud)" {
		t.Fatal("practice answers must read as the candidate's own words")
	}
}
