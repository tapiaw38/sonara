package app

import (
	"strings"
	"testing"
	"time"

	sonaraBackend "sonara/backend/client"
)

// An outlier cannot be classified from a duration alone, so every dimension has
// to survive into the line: a slow upload, a queue wait on OpenAI's side and a
// slow body read are indistinguishable otherwise.
func TestTranscribeDiagnosticsCarriesEveryDimension(t *testing.T) {
	out := sonaraBackend.Transcript{
		UploadBytes: 320 * 1024, Status: 200, ProcessingMS: 900,
		WaitMS: 1400, TotalMS: 1500, RequestID: "req_abc123",
	}
	line := transcribeDiagnostics(out, time.Now().Add(-1600*time.Millisecond))
	for _, want := range []string{"upload=320KB", "status=200", "proc=900ms", "wait=1400ms", "total=1500ms", "req=req_abc123"} {
		if !strings.Contains(line, want) {
			t.Errorf("line is missing %q: %s", want, line)
		}
	}
	// wall covers the whole call including queueing inside Sonara, so it must
	// be reported separately from the gateway's own total.
	if !strings.Contains(line, "wall=16") {
		t.Errorf("wall clock not reported: %s", line)
	}
}

// A failed call is exactly when the diagnostics matter, and OpenAI does not
// always return a request id, so an absent one must not shift the fields.
func TestTranscribeDiagnosticsOnAFailure(t *testing.T) {
	line := transcribeDiagnostics(sonaraBackend.Transcript{Status: 429, WaitMS: 80, TotalMS: 80}, time.Now())
	if !strings.Contains(line, "status=429") {
		t.Errorf("status lost on failure: %s", line)
	}
	if !strings.Contains(line, "req=-") {
		t.Errorf("a missing request id should render as a dash: %s", line)
	}
}
