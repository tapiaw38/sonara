#!/usr/bin/env bash
# Checks Sonara's dependencies, builds it and installs it for the current user
# (~/.local/bin, plus a desktop entry and icon). Package names are for Arch.
# Usage: ./install.sh          check, build and install
#        ./install.sh --check  only report what is missing
set -euo pipefail
cd "$(dirname "$0")"

missing_required=0
ok()   { printf '  \033[32m✓\033[0m %s\n' "$1"; }
need() { printf '  \033[31m✗\033[0m %-28s install: %s\n' "$1" "$2"; missing_required=1; }
want() { printf '  \033[33m-\033[0m %-28s %s\n' "$1" "$2"; }

has() { command -v "$1" >/dev/null 2>&1; }
# Wails and npm may live outside PATH in a fresh shell.
export PATH="$PATH:$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin"

echo "Build:"
for tool in "go:go" "node:nodejs" "npm:npm" "gcc:base-devel" "pkg-config:base-devel"; do
  if has "${tool%%:*}"; then ok "${tool%%:*}"; else need "${tool%%:*}" "pacman -S ${tool#*:}"; fi
done
if has wails; then ok wails; else need wails "go install github.com/wailsapp/wails/v2/cmd/wails@latest"; fi
for lib in "webkit2gtk-4.1:webkit2gtk-4.1" "gtk+-3.0:gtk3"; do
  if pkg-config --exists "${lib%%:*}" 2>/dev/null; then ok "${lib%%:*}"; else need "${lib%%:*}" "pacman -S ${lib#*:}"; fi
done

echo "Runtime:"
for tool in pw-record pw-dump; do
  if has "$tool"; then ok "$tool"; else need "$tool" "pacman -S pipewire pipewire-audio (no audio capture without it)"; fi
done
if has ffmpeg; then ok ffmpeg; else want ffmpeg "optional, pacman -S ffmpeg: noise filter in Practice"; fi
if has pipewire && has pw-cli && [ -e /usr/lib/spa-0.2/aec/libspa-aec-webrtc.so ]; then
  ok "echo cancellation (PipeWire WebRTC)"
else
  want "echo cancellation" "optional, pacman -S pipewire pipewire-audio: talk over the interviewer on speakers"
fi
if has grim; then ok grim; else want grim "optional, pacman -S grim: screen capture"; fi
if has wlrctl; then ok wlrctl; else want wlrctl "optional, pacman -S wlrctl: window focus"; fi
if has mmsg; then ok mmsg; else want mmsg "optional, mangowm-git (AUR): always-on-top on mango"; fi

echo "Local engines (optional, see README):"
whisper="${WHISPER_SERVER_BIN:-$HOME/.local/share/whisper.cpp/build/bin/whisper-server}"
model="${WHISPER_MODEL:-$HOME/.local/share/whisper.cpp/models/ggml-large-v3-turbo-q5_0.bin}"
if [ -x "$whisper" ] && [ -f "$model" ]; then ok "local Whisper"; else want "local Whisper" "not found: cloud transcription only"; fi
sherpa="$HOME/.local/share/sherpa-onnx"
if compgen -G "$sherpa/sherpa-onnx-*/bin/sherpa-onnx-offline-speaker-diarization" >/dev/null \
  && [ -f "$sherpa/sherpa-onnx-pyannote-segmentation-3-0/model.onnx" ] \
  && [ -f "$sherpa/3dspeaker_speech_eres2net_sv_en_voxceleb_16k.onnx" ]; then
  ok "local speaker separation (sherpa-onnx)"
else
  want "local speaker separation" "not found: Gemini key needed to separate speakers"
fi

if [ "$missing_required" -ne 0 ]; then
  echo
  echo "Install the missing (✗) dependencies above, then run ./install.sh again."
  exit 1
fi
[ "${1:-}" = "--check" ] && exit 0

echo
echo "Building..."
wails build -tags webkit2_41

mkdir -p ~/.local/bin ~/.local/share/applications ~/.local/share/icons/hicolor/512x512/apps
# Copy then rename: replacing a running binary in place would fail.
cp build/bin/sonara ~/.local/bin/sonara.new && mv ~/.local/bin/sonara.new ~/.local/bin/sonara
cp build/appicon.png ~/.local/share/icons/hicolor/512x512/apps/sonara.png
cat > ~/.local/share/applications/sonara.desktop <<EOF
[Desktop Entry]
Type=Application
Name=Sonara
Comment=Copiloto de entrevistas y transcripción de reuniones
Exec=$HOME/.local/bin/sonara
Icon=sonara
Terminal=false
Categories=Utility;
EOF
echo "Installed: ~/.local/bin/sonara (restart Sonara if it is open)."
case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) echo "Note: ~/.local/bin is not in your PATH." ;; esac
