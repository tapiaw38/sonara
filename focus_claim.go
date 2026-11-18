package main

import (
	"context"
	"os/exec"
	"time"
)

func (a *App) ClaimWindowFocus() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "wlrctl", "toplevel", "focus", "app-id:sonara").Run()
}
