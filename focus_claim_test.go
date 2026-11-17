package main

import (
	"testing"
	"time"
)

// ClaimWindowFocus must never panic or block past its own timeout, on any
// machine — most CI/dev machines running this test have no wlrctl at all.
func TestClaimWindowFocusNeverPanics(t *testing.T) {
	a := NewApp()
	done := make(chan struct{})
	go func() { a.ClaimWindowFocus(); close(done) }()
	select {
	case <-done:
	case <-timeoutChan(3):
		t.Fatal("ClaimWindowFocus must return on its own, well under 3s")
	}
}

func timeoutChan(seconds int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		for range time.Tick(time.Duration(seconds) * time.Second) {
			close(ch)
			return
		}
	}()
	return ch
}
