#!/usr/bin/env bash
# Force-cleanup for the HEADLESS-1 rig, independent of start.sh. Use this if
# start.sh's terminal was killed before its own trap could run, or if you
# just want everything gone without opening the viewer again. Safe to run
# even when nothing is up: every step no-ops if there is nothing to stop.
set -uo pipefail

RUN_DIR="${XDG_RUNTIME_DIR:-/tmp}/sonara-screenshare"
WAYVNC_PIDFILE="$RUN_DIR/wayvnc.pid"
LOCAL_BIN="$HOME/.local/bin"

# Unconditional, not gated on "did we fall back to ~/.local/bin": that
# directory is commonly already on PATH, so `command -v wayvncctl` can find
# the local build directly without ever taking a fallback branch — see
# start.sh for the same fix and why gating this was the original bug.
if [ -d "$HOME/.local/lib" ]; then
	export LD_LIBRARY_PATH="$HOME/.local/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi

WAYVNCCTL_BIN="$(command -v wayvncctl || true)"
if [ -z "$WAYVNCCTL_BIN" ] && [ -x "$LOCAL_BIN/wayvncctl" ]; then
	WAYVNCCTL_BIN="$LOCAL_BIN/wayvncctl"
fi

if [ -n "$WAYVNCCTL_BIN" ] && "$WAYVNCCTL_BIN" wayvnc-exit >/dev/null 2>&1; then
	echo "screenshare: wayvnc detenido via wayvncctl."
elif [ -f "$WAYVNC_PIDFILE" ]; then
	pid="$(cat "$WAYVNC_PIDFILE")"
	if kill "$pid" 2>/dev/null; then
		echo "screenshare: wayvnc (pid $pid) detenido."
	fi
fi
rm -f "$WAYVNC_PIDFILE"

# Named generically, not "HEADLESS-1": Mango's create_virtual_output only
# honors a requested name the first time in a session, after which it falls
# back to wlroots' own counter (HEADLESS-2, -3, ...) — see start.sh and
# README.md. destroy_all_virtual_output removes every headless output
# regardless of name, so matching any of them here is correct.
existing="$(command -v wlr-randr >/dev/null 2>&1 && wlr-randr 2>/dev/null | grep -oE '^HEADLESS-[0-9]+' | sort -u || true)"
if [ -n "$existing" ]; then
	mmsg dispatch destroy_all_virtual_output >/dev/null 2>&1
	echo "screenshare: destruido(s): $(echo "$existing" | tr '\n' ' ')"
else
	echo "screenshare: no había ningún output headless presente."
fi
