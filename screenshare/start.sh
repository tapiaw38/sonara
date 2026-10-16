#!/usr/bin/env bash
# Starts the HEADLESS-1 screen-share rig: a virtual output Mango already
# supports natively, a localhost-only VNC server bound to just that output,
# and a local viewer window so you can see and control what you're about to
# share without eDP-2 (the real screen, where Sonara lives) ever being it.
#
# Run this, present inside the viewer window, pick HEADLESS-1 in Meet/Teams
# (or let the portal config do it for you — see ../README.md), then close the
# viewer window or Ctrl+C here to tear everything down.
set -euo pipefail

# What we ask Mango for. It only reliably gets this name the first time a
# virtual output is created since Mango started — every create after a
# destroy_all_virtual_output falls back silently to wlroots' own counter
# (HEADLESS-2, -3, ...), ignoring the requested name. Confirmed reproducible
# across 3 cycles, not a timing issue (tested with up to 1.5s delays). This
# is why OUTPUT_NAME below is detected from reality, never assumed.
REQUESTED_NAME="HEADLESS-1"
VNC_PORT="5900"
RUN_DIR="${XDG_RUNTIME_DIR:-/tmp}/sonara-screenshare"
WAYVNC_LOG="$RUN_DIR/wayvnc.log"
WAYVNC_PIDFILE="$RUN_DIR/wayvnc.pid"
LOCAL_BIN="$HOME/.local/bin"

mkdir -p "$RUN_DIR"

# build-wayvnc.sh installs libaml/libneatvnc under ~/.local/lib, which is not
# on the dynamic linker's default search path — without this the local
# wayvnc dies instantly with "cannot open shared object file". This has to
# be unconditional: ~/.local/bin is commonly already on PATH (it is here),
# so `command -v wayvnc` finds the local build directly and never takes a
# "fallback" branch to gate this on — gating it that way is exactly the bug
# that shipped first (wayvnc died silently until this was unconditional).
if [ -d "$HOME/.local/lib" ]; then
	export LD_LIBRARY_PATH="$HOME/.local/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
fi

# A system wayvnc (if one ever gets installed via pacman) is preferred; the
# user-local one built by build-wayvnc.sh is the fallback that exists
# specifically to avoid that package's forced ffmpeg dependency — see
# README.md "Bloqueo actual: pacman".
WAYVNC_BIN="$(command -v wayvnc || true)"
if [ -z "$WAYVNC_BIN" ] && [ -x "$LOCAL_BIN/wayvnc" ]; then
	WAYVNC_BIN="$LOCAL_BIN/wayvnc"
fi
WAYVNCCTL_BIN="$(command -v wayvncctl || true)"
if [ -z "$WAYVNCCTL_BIN" ] && [ -x "$LOCAL_BIN/wayvncctl" ]; then
	WAYVNCCTL_BIN="$LOCAL_BIN/wayvncctl"
fi

# tigervnc's vncviewer is tried first (it's what the README documents in
# detail), gtk-vnc's gvncviewer is the fallback actually present on systems
# where tigervnc is blocked by the same pacman conflict wayvnc hit — see
# README.md. Their address syntax differs: vncviewer takes host::port
# (raw port), gvncviewer takes host:display (port - 5900).
VIEWER_CMD=""
if command -v vncviewer >/dev/null 2>&1; then
	VIEWER_CMD="vncviewer 127.0.0.1::${VNC_PORT}"
elif command -v gvncviewer >/dev/null 2>&1; then
	VIEWER_CMD="gvncviewer 127.0.0.1:0"
fi

if [ -z "$WAYVNC_BIN" ]; then
	echo "no hay wayvnc ni en PATH ni en $LOCAL_BIN/wayvnc." >&2
	echo "corré screenshare/build-wayvnc.sh primero (ver README.md)." >&2
	exit 1
fi
if [ -z "$VIEWER_CMD" ]; then
	echo "no hay visor VNC (vncviewer ni gvncviewer) en PATH." >&2
	exit 1
fi
for bin in mmsg wlr-randr; do
	if ! command -v "$bin" >/dev/null 2>&1; then
		echo "falta '$bin' en PATH. Ver screenshare/README.md (sección Instalación)." >&2
		exit 1
	fi
done

if [ -z "${MANGO_INSTANCE_SIGNATURE:-}" ]; then
	echo "MANGO_INSTANCE_SIGNATURE no está seteada: esto no corre dentro de una sesión de Mango." >&2
	exit 1
fi

headless_outputs() {
	# grep exits 1 when nothing matches — the normal "no virtual output yet"
	# case. Under set -e that would kill the script silently (straight to the
	# cleanup trap, no error message) before it ever creates one.
	wlr-randr 2>/dev/null | grep -oE '^HEADLESS-[0-9]+' | sort -u || true
}

cleanup() {
	# wayvnc-exit is the clean shutdown path (closes VNC clients first); fall
	# back to the raw PID if wayvncctl can't reach it for any reason.
	if [ -f "$WAYVNC_PIDFILE" ]; then
		pid="$(cat "$WAYVNC_PIDFILE")"
		{ [ -n "$WAYVNCCTL_BIN" ] && "$WAYVNCCTL_BIN" wayvnc-exit; } >/dev/null 2>&1 \
			|| kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
		rm -f "$WAYVNC_PIDFILE"
	fi
	if [ -n "$(headless_outputs)" ]; then
		mmsg dispatch destroy_all_virtual_output >/dev/null 2>&1 || true
	fi
	echo "screenshare: limpio (${OUTPUT_NAME:-virtual output} destruida, wayvnc detenido)."
}
trap cleanup EXIT INT TERM

existing="$(headless_outputs)"
if [ -n "$existing" ]; then
	OUTPUT_NAME="$(echo "$existing" | head -1)"
	echo "screenshare: ya hay un output headless (${OUTPUT_NAME}), lo reutilizo."
else
	before="$(headless_outputs)"
	result="$(mmsg dispatch "create_virtual_output,${REQUESTED_NAME}")"
	if ! echo "$result" | grep -q '"success":true'; then
		echo "no se pudo crear ${REQUESTED_NAME}: $result" >&2
		exit 1
	fi
	after="$(headless_outputs)"
	OUTPUT_NAME="$(comm -13 <(echo "$before") <(echo "$after"))"
	if [ -z "$OUTPUT_NAME" ]; then
		echo "mango dijo success pero no aparece ningún HEADLESS-* nuevo en wlr-randr." >&2
		exit 1
	fi
	echo "screenshare: output creado, 1920x1080, invisible en eDP-2."
	if [ "$OUTPUT_NAME" != "$REQUESTED_NAME" ]; then
		echo "screenshare: AVISO — pedí '${REQUESTED_NAME}' pero Mango asignó '${OUTPUT_NAME}'" >&2
		echo "  (pasa después del primer ciclo create/destroy en la sesión, ver README.md)." >&2
		echo "  Si el portal está configurado con output_name=${REQUESTED_NAME}, no va a" >&2
		echo "  encontrar '${OUTPUT_NAME}' — actualizá ~/.config/xdg-desktop-portal-wlr/config" >&2
		echo "  a output_name=${OUTPUT_NAME} y reiniciá el servicio del portal." >&2
	fi
fi

"$WAYVNC_BIN" -o "$OUTPUT_NAME" 127.0.0.1 "$VNC_PORT" >"$WAYVNC_LOG" 2>&1 &
echo $! >"$WAYVNC_PIDFILE"

# Give wayvnc a moment to bind the port before the viewer tries to connect.
for _ in $(seq 1 20); do
	if ! kill -0 "$(cat "$WAYVNC_PIDFILE")" 2>/dev/null; then
		echo "wayvnc murió al arrancar, ver $WAYVNC_LOG" >&2
		exit 1
	fi
	grep -q "Listening for connections" "$WAYVNC_LOG" 2>/dev/null && break
	sleep 0.2
done

echo "screenshare: wayvnc (${WAYVNC_BIN}) sirviendo ${OUTPUT_NAME} en 127.0.0.1:${VNC_PORT} (solo localhost)."
echo "screenshare: abriendo visor (${VIEWER_CMD%% *}). Cerrá la ventana (o Ctrl+C acá) para terminar y limpiar."

$VIEWER_CMD
