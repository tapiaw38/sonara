// Package fsutil holds file helpers.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes beside the destination and renames, so a failed or
// interrupted export never leaves a half-written .txt where a good one was.
func WriteAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".sonara-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	// Transcripts are private: same mode as the database.
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
