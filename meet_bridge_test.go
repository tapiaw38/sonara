package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMatchMeetNames(t *testing.T) {
	var m meetSpeaking
	// Walter's captions from 2s to 6s, Ana's from 10s to 14s (before the lag shift).
	for ts := int64(2000); ts <= 6000; ts += 500 {
		m.add("s", "Walter", ts)
	}
	for ts := int64(10000); ts <= 14000; ts += 500 {
		m.add("s", "Ana", ts)
	}
	spans := m.snapshot("s")
	if len(spans) != 2 {
		t.Fatalf("want 2 joined spans, got %+v", spans)
	}
	segments := []TranscriptSegment{
		{Track: trackOutput, SpeakerKey: "1", StartMS: 1000, EndMS: 5000, Status: segmentOK},
		{Track: trackOutput, SpeakerKey: "2", StartMS: 9000, EndMS: 13000, Status: segmentOK},
		{Track: trackOutput, SpeakerKey: "3", StartMS: 20000, EndMS: 25000, Status: segmentOK}, // nobody in Meet
		{Track: trackMicrophone, SpeakerKey: "1", StartMS: 1000, EndMS: 5000, Status: segmentOK},
	}
	got := matchMeetNames(segments, spans)
	if got["1"] != "Walter" || got["2"] != "Ana" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if names := m.names("s"); len(names) != 2 || names[0] != "Walter" {
		t.Fatalf("names %v", names)
	}
	if m.snapshot("other") != nil {
		t.Fatal("spans leaked to another session")
	}
}

func TestMeetSpeakingHandlerRejectsWebPages(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodPost, "/meet/speaking", strings.NewReader(`{"name":"X"}`))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	a.meetSpeakingHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/meet/speaking", strings.NewReader(`{"name":"X"}`))
	req.Header.Set("Origin", "chrome-extension://abc")
	rec = httptest.NewRecorder()
	a.meetSpeakingHandler(rec, req)
	if rec.Code != http.StatusConflict { // not recording
		t.Fatalf("want 409, got %d", rec.Code)
	}
}
