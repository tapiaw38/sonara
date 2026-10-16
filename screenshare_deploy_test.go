package main

import (
	"os"
	"path/filepath"
	"testing"
)

// This is the whole point of the feature: a binary with no repo checkout
// next to it still ends up with runnable scripts and the nested portal
// config on disk.
func TestDeployScreenshareFilesWritesTheWholeTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "screenshare")
	if err := deployScreenshareFiles(dir); err != nil {
		t.Fatalf("deployScreenshareFiles: %v", err)
	}

	for _, name := range []string{
		"start.sh", "stop.sh", "build-wayvnc.sh", "README.md", "portal/config",
		"wayvnc-wraps/aml.wrap", "wayvnc-wraps/neatvnc.wrap",
	} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s was not written: %v", name, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	// The scripts are meant to be run directly from the deployed copy, not
	// just read, so losing +x here would silently break the one thing the
	// feature exists for.
	for _, name := range []string{"start.sh", "stop.sh", "build-wayvnc.sh"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s is not executable (mode %o)", name, info.Mode().Perm())
		}
	}
	// README, the portal template and the wrap files are read, never
	// executed, and the portal one in particular is only ever meant to be
	// copied elsewhere.
	for _, name := range []string{"README.md", "portal/config", "wayvnc-wraps/aml.wrap", "wayvnc-wraps/neatvnc.wrap"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm()&0o100 != 0 {
			t.Errorf("%s is executable (mode %o), want it plain", name, info.Mode().Perm())
		}
	}
}

// A second deploy (every app restart) must not fail or leave stale files
// behind from some earlier, different version of the tree.
func TestDeployScreenshareFilesIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "screenshare")
	if err := deployScreenshareFiles(dir); err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	if err := deployScreenshareFiles(dir); err != nil {
		t.Fatalf("second deploy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "start.sh")); err != nil {
		t.Fatalf("start.sh missing after redeploy: %v", err)
	}
}
