package app

import (
	"io/fs"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// Run opens the Sonara window and blocks until it closes. assets is the built
// frontend and icon the window icon; both are embedded by package main, which
// is the only place that can reach those directories.
func Run(assets fs.FS, icon []byte) error {
	app := NewApp()
	return wails.Run(&options.App{
		Title:            "Sonara",
		Width:            1024,
		Height:           768,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		Linux:            &linux.Options{Icon: icon, WindowIsTranslucent: true},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []any{app},
	})
}
