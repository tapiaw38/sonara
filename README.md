# Sonara

Local-first desktop assistant for interviews and meetings, built with Wails
(Go + Vue 3/TypeScript) for Linux on Wayland.

## Features

- **Interview copilot:** listens to the call's audio, transcribes it live and
  suggests replies. It can also capture the screen and analyze it (e.g. a
  coding question).
- **Voice practice:** a mock interview with a spoken interviewer you can
  interrupt by tapping the orb or talking over it.
- **Meeting transcription:** records your microphone and the meeting's audio
  as separate tracks, separates the participants by voice (locally or with
  Gemini) and names them from the conversation itself.
- **Google Meet names:** an optional Chrome extension
  ([`extension/meet`](extension/meet/README.md)) tells Sonara who is speaking
  in Meet, so participants get their real names.

Sessions and transcripts are stored locally in SQLite.

## Requirements

Linux with Wayland and PipeWire. Package names below are for Arch Linux.

### Build

| Need | Arch package | Notes |
|------|--------------|-------|
| Go | `go` | |
| Node.js + npm | `nodejs`, `npm` | |
| Wails CLI | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` | |
| C compiler, pkg-config | `base-devel` | the voice detector (webrtcvad) uses cgo |
| WebKitGTK 4.1, GTK 3 | `webkit2gtk-4.1`, `gtk3` | also needed at runtime |

### Runtime

| Tool | Arch package | Used for | Without it |
|------|--------------|----------|------------|
| `pw-record`, `pw-dump` | `pipewire`, `pipewire-audio` | all audio capture and device list | nothing can be recorded |
| `ffmpeg` | `ffmpeg` | noise filter before voice detection in Practice | raw mic is used |
| `pipewire`, `pw-cli`, WebRTC echo canceller | `pipewire`, `pipewire-audio` | echo cancellation in Practice, so you can talk over the interviewer | it hears itself on speakers |
| `grim` | `grim` | screen capture | no screen analysis |
| `wlrctl` | `wlrctl` | focusing the Sonara window | |
| `mmsg` | `mangowm-git` (AUR) | always-on-top (mango compositor only) | toggle does nothing |

### Transcription and speaker separation

Transcription runs in the cloud by default; the local engines are optional.

- **OpenAI API key** (set in the app, or `OPENAI_API_KEY`): transcription, live
  transcription, replies and voice. Required.
- **DeepSeek key** (set in the app, or `DEEPSEEK_API_KEY`): optional, cheaper text replies.
- **Gemini key** (set in the app): optional, cloud speaker separation for meetings (`GEMINI_MODEL`
  picks the model).
- **Local Whisper** (provider "local"): [whisper.cpp](https://github.com/ggml-org/whisper.cpp)
  built with its server, in `~/.local/share/whisper.cpp`:
  - binary `build/bin/whisper-server` (built here with CUDA, `-DGGML_CUDA=1`),
  - model `models/ggml-large-v3-turbo-q5_0.bin`
    (`./models/download-ggml-model.sh large-v3-turbo-q5_0`).
  - Override with `WHISPER_SERVER_BIN` and `WHISPER_MODEL`.
- **Local speaker separation**: [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx/releases),
  unpacked in `~/.local/share/sherpa-onnx`:
  - `sherpa-onnx-v*-linux-x64-static/` (provides `bin/sherpa-onnx-offline-speaker-diarization`),
  - `sherpa-onnx-pyannote-segmentation-3-0/model.onnx` (segmentation model),
  - `3dspeaker_speech_eres2net_sv_en_voxceleb_16k.onnx` (speaker embedding model).
  - `SONARA_DIARIZE_THRESHOLD` tunes how readily voices are split.

## Development

```sh
wails dev -tags webkit2_41
```

Backend tests: `go test ./...`. Frontend format: `cd frontend && npm run format`.

## Build and install

```sh
wails build -tags webkit2_41
cp build/bin/sonara ~/.local/bin/sonara
```

## Project layout

- `*.go` (root): the Wails app: audio capture, meetings, diarization, practice.
- `backend/`: the in-process assistant API. See [`backend/README.md`](backend/README.md).
- `frontend/`: the Vue UI.
- `extension/meet/`: the Google Meet Chrome extension.
