package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The screenshare scripts are maintained in this repo, but they're only
// useful once they exist outside it: on a machine where Sonara was installed
// from a built binary, there is no repo checkout to run them from. Embedding
// at compile time and writing them out at every startup is what makes
// "compilar o instalar la app" actually produce usable files, instead of
// just documentation that only works for someone editing this repo.
//
//go:embed screenshare
var screenshareFiles embed.FS

// screenshareDeployDir follows store.go's precedent of using the config
// directory for everything Sonara ships, not just user-editable settings.
func screenshareDeployDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "sonara", "screenshare"), nil
}

// deployScreenshareFiles writes the embedded screenshare/ tree to dir,
// overwriting on every call. There is nothing here for a user to hand-edit —
// portal/config is a template meant to be copied elsewhere before activation
// — so always refreshing is simpler than tracking versions, and it is what
// keeps the deployed copy in sync with whatever build of Sonara wrote it.
func deployScreenshareFiles(dir string) error {
	return fs.WalkDir(screenshareFiles, "screenshare", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "screenshare")
		rel = strings.TrimPrefix(rel, "/")
		target := filepath.Join(dir, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := screenshareFiles.ReadFile(path)
		if err != nil {
			return err
		}
		// go:embed carries no permission bits; the scripts need +x to be
		// runnable straight from where they land.
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}

// deployScreenshareFilesToDefaultLocation is the entry point startup() calls.
// It never fails the caller: a user who never touches this feature should
// never see it as a reason Sonara failed to start.
func deployScreenshareFilesToDefaultLocation() (string, error) {
	dir, err := screenshareDeployDir()
	if err != nil {
		return "", err
	}
	if err := deployScreenshareFiles(dir); err != nil {
		return "", err
	}
	return dir, nil
}
