package meetbridge

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"sonara/internal/storage"
)

func TestMatchNames(t *testing.T) {
	var m Tracker
	// Walter's captions from 2s to 6s, Ana's from 10s to 14s (before the lag shift).
	for ts := int64(2000); ts <= 6000; ts += 500 {
		m.Add("s", "Walter", ts)
	}
	for ts := int64(10000); ts <= 14000; ts += 500 {
		m.Add("s", "Ana", ts)
	}
	spans := m.Snapshot("s")
	if len(spans) != 2 {
		t.Fatalf("want 2 joined spans, got %+v", spans)
	}
	segments := []storage.TranscriptSegment{
		{Track: storage.TrackOutput, SpeakerKey: "1", StartMS: 1000, EndMS: 5000, Status: storage.SegmentOK},
		{Track: storage.TrackOutput, SpeakerKey: "2", StartMS: 9000, EndMS: 13000, Status: storage.SegmentOK},
		{Track: storage.TrackOutput, SpeakerKey: "3", StartMS: 20000, EndMS: 25000, Status: storage.SegmentOK}, // nobody in Meet
		{Track: storage.TrackMicrophone, SpeakerKey: "1", StartMS: 1000, EndMS: 5000, Status: storage.SegmentOK},
	}
	got := MatchNames(segments, spans)
	if got["1"] != "Walter" || got["2"] != "Ana" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if names := m.Names("s"); len(names) != 2 || names[0] != "Walter" {
		t.Fatalf("names %v", names)
	}
	if m.Snapshot("other") != nil {
		t.Fatal("spans leaked to another session")
	}
}

type idle struct{}

func (idle) ActiveRecording() (string, time.Time, bool) { return "", time.Time{}, false }

func TestHandlerRejectsWebPages(t *testing.T) {
	t.Setenv("SONARA_MEET_EXTENSION_ID", "")
	handler := Handler(idle{}, &Tracker{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/meet/speaking", strings.NewReader(`{"name":"X"}`))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/meet/speaking", strings.NewReader(`{"name":"X"}`))
	req.Header.Set("Origin", "chrome-extension://abc")
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusConflict { // not recording
		t.Fatalf("want 409, got %d", rec.Code)
	}
}

func TestHandlerPinsExtensionOrigin(t *testing.T) {
	t.Setenv("SONARA_MEET_EXTENSION_ID", "abc")
	handler := Handler(idle{}, &Tracker{}, nil)
	for _, tc := range []struct {
		origin string
		want   int
	}{
		{"chrome-extension://abc", http.StatusConflict},
		{"chrome-extension://abcd", http.StatusForbidden},
		{"chrome-extension://other", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodPost, "/meet/speaking", strings.NewReader(`{"name":"X"}`))
		req.Header.Set("Origin", tc.origin)
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != tc.want {
			t.Errorf("origin %q: status = %d, want %d", tc.origin, rec.Code, tc.want)
		}
	}
}

func TestTrackerCapsNamesAndSpansPerSession(t *testing.T) {
	var tracker Tracker
	for i := range 50 {
		if !tracker.Add("first", strconv.Itoa(i), 0) {
			t.Fatalf("name %d was dropped before the limit", i)
		}
	}
	if tracker.Add("first", "extra", 0) || len(tracker.Names("first")) != 50 || len(tracker.Snapshot("first")) != 50 {
		t.Fatal("tracker stored a 51st name")
	}
	for i := 1; i <= 4951; i++ {
		tracker.Add("first", "0", int64(i)*3000)
	}
	if got := len(tracker.Snapshot("first")); got != 5000 {
		t.Fatalf("stored %d spans, want 5000", got)
	}
	if !tracker.Add("second", "extra", 0) || len(tracker.Snapshot("second")) != 1 {
		t.Fatal("limits did not reset for the next session")
	}
}
