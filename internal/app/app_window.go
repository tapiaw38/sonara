package app

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

type mangoClient struct {
	IsFloating bool `json:"is_floating"`
	IsOverlay  bool `json:"is_overlay"`
}

func overlayPlan(isOverlay, isFloating, weFloated bool) (cmds []string, nowFloated bool) {
	if isOverlay {
		cmds = append(cmds, "toggleoverlay")
		if weFloated {
			cmds = append(cmds, "togglefloating")
		}
		return cmds, false
	}
	if !isFloating {
		cmds = append(cmds, "togglefloating")
		weFloated = true
	}
	return append(cmds, "toggleoverlay"), weFloated
}

func (a *App) ToggleAlwaysOnTop() bool {
	out, err := exec.Command("mmsg", "get", "focusing-client").Output()
	if err != nil {
		return false
	}
	var c mangoClient
	if err := json.Unmarshal(out, &c); err != nil {
		return false
	}

	cmds, nowFloated := overlayPlan(c.IsOverlay, c.IsFloating, a.floatedForOverlay)
	for _, cmd := range cmds {
		if err := exec.Command("mmsg", "dispatch", cmd).Run(); err != nil {
			return false
		}
	}

	a.mu.Lock()
	a.floatedForOverlay = nowFloated
	a.mu.Unlock()
	return true
}

func (a *App) ClaimWindowFocus() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "wlrctl", "toplevel", "focus", "app-id:sonara").Run()
}
