package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func node(name string, serial float64, state string) string {
	return fmt.Sprintf(`{"type":"PipeWire:Interface:Node","id":%d,"info":{"state":%q,"props":{"media.class":"Stream/Output/Audio","application.name":%q,"object.serial":%v}}}`,
		int(serial), state, name, serial)
}

func TestPickChromeSerialPrefersRunningNewestChromeNode(t *testing.T) {
	dump := "[" + strings.Join([]string{
		node("Firefox", 301, "running"),
		node("Google Chrome", 328, "idle"),
		node("Google Chrome", 461, "running"),
		node("Google Chrome", 402, "running"),
	}, ",") + "]"

	serial, err := pickChromeSerial([]byte(dump))
	if err != nil {
		t.Fatalf("pickChromeSerial: %v", err)
	}
	if serial != "461" {
		t.Errorf("serial = %q, want 461 (newest running Chrome node)", serial)
	}
}

func TestPickChromeSerialFallsBackToIdleChrome(t *testing.T) {
	dump := "[" + node("Google Chrome", 328, "idle") + "]"
	serial, err := pickChromeSerial([]byte(dump))
	if err != nil || serial != "328" {
		t.Errorf("serial = %q, err = %v, want 328", serial, err)
	}
}

func TestPickChromeSerialWithoutChrome(t *testing.T) {
	dump := "[" + node("Firefox", 301, "running") + "]"
	if _, err := pickChromeSerial([]byte(dump)); err == nil {
		t.Error("expected an error when Chrome has no output stream")
	}
}

func TestManualSendHoldsSegmentUntilRequested(t *testing.T) {
	app := NewApp()
	app.SetManualSend(true)
	app.processMeetSegment(make([]byte, meetSampleRate*2)) // 1s of silence-valued PCM

	if got := app.ListeningStatus().PendingSeconds; got != 1 {
		t.Errorf("pendingSeconds = %v, want 1", got)
	}
	if !app.DiscardPendingSegment() {
		t.Error("discard should report success")
	}
	if got := app.ListeningStatus().PendingSeconds; got != 0 {
		t.Errorf("pendingSeconds after discard = %v, want 0", got)
	}
	if app.SendPendingSegment() {
		t.Error("send should report false when nothing is pending")
	}
}

func TestWavFromPCMHeader(t *testing.T) {
	wav := wavFromPCM(make([]byte, 3200))
	if !bytes.Equal(wav[0:4], []byte("RIFF")) {
		t.Errorf("riff id = %q", wav[0:4])
	}
	if !bytes.Equal(wav[8:12], []byte("WAVE")) {
		t.Errorf("wave id = %q", wav[8:12])
	}
	if !bytes.Equal(wav[12:16], []byte("fmt ")) {
		t.Errorf("fmt chunk id = %q", wav[12:16])
	}
	if !bytes.Equal(wav[36:40], []byte("data")) {
		t.Errorf("data chunk id = %q", wav[36:40])
	}
	if len(wav) != 44+3200 {
		t.Errorf("length = %d", len(wav))
	}
}
