// Command sonara is the desktop app. Everything lives in internal/app; this
// file only embeds the frontend and the icon, which must sit beside it.
package main

import (
	"embed"
	"os"

	"sonara/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	if err := app.Run(assets, appIcon); err != nil {
		println("Error:", err.Error())
		os.Exit(1)
	}
}
