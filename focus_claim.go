package main

import (
	"context"
	"os/exec"
	"time"
)

// ClaimWindowFocus asks the Wayland compositor to give this window keyboard
// focus right now. It exists because of a real bug: with a translucent
// window (main.go's WindowIsTranslucent, needed for ghost mode's actual
// see-through effect) GTK declares no opaque region, and at least mango's
// focus-follows-mouse ("sloppy_focus") then does not treat hovering over
// Sonara as entering it — the window stays fully interactive (a click, or
// this call, focuses it instantly), it just never gets focus from the mouse
// alone. Without focus, the compositor's own close keybind (bound to "the
// focused client") silently targets whatever was focused before, which reads
// as "Sonara doesn't close".
//
// The frontend calls this once per mouseenter (App.vue), so hovering behaves
// like it does for every other window despite the one declared difference.
// Uses wlrctl (the generic wlr-foreign-toplevel protocol, not mango-specific)
// so this degrades to a harmless no-op on any system without it — X11,
// non-wlroots Wayland compositors, or a missing binary.
func (a *App) ClaimWindowFocus() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Errors are never reported: a compositor that cannot do this has no
	// worse a close experience than before this existed, and this runs often
	// enough that logging every miss would just be noise.
	exec.CommandContext(ctx, "wlrctl", "toplevel", "focus", "app-id:sonara").Run()
}
