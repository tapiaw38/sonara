#!/usr/bin/env bash
# Builds wayvnc entirely in user space: no pacman, no root, no system
# libraries touched. This exists because the packaged wayvnc in `extra`
# hard-links neatvnc's optional H.264 encoder, which pulls ffmpeg 9 and
# breaks ABI with chromaprint/thunderbird/wf-recorder/vlc-plugin-ffmpeg —
# a real partial-upgrade conflict, not a stale cache (see README.md,
# "Bloqueo actual: pacman"). Disabling that one optional feature at build
# time removes ffmpeg from the dependency graph entirely; every other
# encoding (ZRLE, Tight, raw) still builds in.
#
# Mechanism: wayvnc's own meson.build already does
#   subproject('neatvnc', ...) / subproject('aml', ...)
# with a pkg-config fallback. Dropping matching .wrap files into wayvnc's
# subprojects/ directory is the upstream-intended way to vendor them — this
# script does not invent a build graph, it feeds the one wayvnc ships.
#
# Every dependency wayvnc/neatvnc/aml need, minus ffmpeg, is already present
# on this system via pkg-config (verified: pixman-1, xkbcommon, wayland-client,
# jansson, libdrm, gbm, zlib, gnutls, nettle, hogweed, gmp, wayland-protocols).
# Nothing here installs a package.
set -euo pipefail

WAYVNC_TAG="v0.10.2"
PREFIX="${WAYVNC_PREFIX:-$HOME/.local}"
SRC_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/sonara/wayvnc-src"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for tool in meson ninja git pkg-config cc; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "falta '$tool' en PATH. Sin esto no se puede compilar nada acá — no es" >&2
		echo "un paquete nuevo a instalar por este script, revisá tu sistema base." >&2
		exit 1
	fi
done

missing_pc=""
for pc in pixman-1 xkbcommon wayland-client jansson libdrm gbm zlib wayland-protocols; do
	pkg-config --exists "$pc" 2>/dev/null || missing_pc="$missing_pc $pc"
done
if [ -n "$missing_pc" ]; then
	echo "faltan estas libs (pkg-config no las encuentra):$missing_pc" >&2
	echo "esto no se resuelve compilando wayvnc — son dependencias de abajo." >&2
	exit 1
fi

if [ -d "$SRC_DIR/.git" ]; then
	echo "build-wayvnc: reusando checkout en $SRC_DIR"
else
	rm -rf "$SRC_DIR"
	git clone --branch "$WAYVNC_TAG" --depth 1 \
		https://github.com/any1/wayvnc.git "$SRC_DIR"
fi

mkdir -p "$SRC_DIR/subprojects"
cp "$HERE/wayvnc-wraps/aml.wrap" "$SRC_DIR/subprojects/aml.wrap"
cp "$HERE/wayvnc-wraps/neatvnc.wrap" "$SRC_DIR/subprojects/neatvnc.wrap"

cd "$SRC_DIR"

# -Dman-pages=disabled: scdoc isn't installed and the man pages aren't needed
# to run the binary. -Dtests/neatvnc:tests=false: skip unit test binaries,
# this is not a dev checkout. -Dneatvnc:h264=disabled: the one flag that
# actually matters — see header comment.
MESON_FLAGS=(
	--prefix="$PREFIX"
	-Dman-pages=disabled
	-Dtests=false
	-Dneatvnc:h264=disabled
	-Dneatvnc:tests=false
	-Dneatvnc:examples=false
	-Dneatvnc:benchmarks=false
)
if [ -d build ]; then
	meson setup build --reconfigure "${MESON_FLAGS[@]}"
else
	meson setup build "${MESON_FLAGS[@]}"
fi

ninja -C build
ninja -C build install

echo "build-wayvnc: instalado en $PREFIX/bin/wayvnc"
# Ground truth, not a guess about meson's internal build layout: check what
# the actual installed binary links against, with the same LD_LIBRARY_PATH
# start.sh will use — libaml/libneatvnc live in $PREFIX/lib, which is not on
# the linker's default search path, so a plain `ldd` here would misreport
# them as missing.
LDD_OUT="$(LD_LIBRARY_PATH="$PREFIX/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" ldd "$PREFIX/bin/wayvnc" 2>&1)"
if echo "$LDD_OUT" | grep -qi "libav"; then
	echo "build-wayvnc: ADVERTENCIA — el binario quedó linkeado contra ffmpeg (libav*). Revisar -Dneatvnc:h264." >&2
elif echo "$LDD_OUT" | grep -qi "not found"; then
	echo "build-wayvnc: ADVERTENCIA — faltan libs en runtime:" >&2
	echo "$LDD_OUT" | grep -i "not found" >&2
else
	echo "build-wayvnc: confirmado — sin ffmpeg, todas las libs resuelven con LD_LIBRARY_PATH=$PREFIX/lib."
fi
echo "build-wayvnc: para corridas manuales (no via start.sh) necesitás:"
echo "  export LD_LIBRARY_PATH=\"$PREFIX/lib:\$LD_LIBRARY_PATH\""
