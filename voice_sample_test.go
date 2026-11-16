package main

import (
	"os"
	"path/filepath"
	"testing"
)

// withVoiceSample points voiceSamplePath at a throwaway directory (via
// XDG_CONFIG_HOME, which os.UserConfigDir honors on Linux) and writes data
// there, so tests never touch the real user config directory.
func withVoiceSample(t *testing.T, data []byte) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := voiceSamplePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHasVoiceSampleAndDelete(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := &App{}
	if a.HasVoiceSample() {
		t.Fatal("a fresh config dir must report no sample")
	}
	withVoiceSample(t, []byte("algo"))
	if !a.HasVoiceSample() {
		t.Fatal("after writing a sample, HasVoiceSample must report true")
	}
	data, err := readVoiceSample()
	if err != nil || string(data) != "algo" {
		t.Fatalf("got %q, %v", data, err)
	}
	if err := a.DeleteVoiceSample(); err != nil {
		t.Fatal(err)
	}
	if a.HasVoiceSample() {
		t.Fatal("after deleting, HasVoiceSample must report false")
	}
	if err := a.DeleteVoiceSample(); err != nil {
		t.Fatalf("deleting an already-absent sample must be a no-op, got %v", err)
	}
}

func TestReadVoiceSampleWithoutOneRecordedErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := readVoiceSample(); err == nil {
		t.Fatal("reading a sample that was never recorded must error, not return empty bytes")
	}
}

func TestRecordVoiceSampleRequiresAMicrophone(t *testing.T) {
	a := NewApp()
	if err := a.RecordVoiceSample(); err == nil {
		t.Fatal("recording without a selected microphone must error")
	}
}
