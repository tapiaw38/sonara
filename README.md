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

- **Kokoro local voice for Practice** (Linux x86-64): select **Kokoro local (gratis)**
  in **Configuración → Uso → Voz (Práctica)**. The int8 v1.0 archive is about
  127 MiB. It includes Spanish `ef_dora` (28) and US English `af_bella` (2).
  TTS uses sherpa-onnx 1.12.40 for its Spanish phonemizer and can coexist with
  sherpa-onnx 2.x for speaker separation. Only synthesis runs locally by
  selecting Kokoro. Practice still uses the configured live transcriber and text
  provider.

  ```sh
  dir="$HOME/.local/share/sherpa-onnx"
  cli=sherpa-onnx-v1.12.40-linux-x64-static.tar.bz2
  shared=sherpa-onnx-v1.12.40-linux-x64-shared-lib.tar.bz2
  model=kokoro-int8-multi-lang-v1_0.tar.bz2
  mkdir -p "$dir"
  gh release download v1.12.40 -R k2-fsa/sherpa-onnx -p "$cli" -D "$dir" --skip-existing
  gh release download tts-models -R k2-fsa/sherpa-onnx -p "$model" -D "$dir" --skip-existing
  tar -xjf "$dir/$cli" -C "$dir"
  tar -xjf "$dir/$model" -C "$dir"
  gh release download v1.12.40 -R k2-fsa/sherpa-onnx -p "$shared" -D "$dir" --skip-existing
  tar -xjf "$dir/$shared" -C "$dir"
  cpu_lib="$dir/${shared%.tar.bz2}/lib"
  cc -O2 -I"$dir/${cli%.tar.bz2}/include" \
    backend/internal/adapters/web/integrations/kokoro/worker/main.c \
    -L"$cpu_lib" -Wl,-rpath,"$cpu_lib" -lsherpa-onnx-c-api \
    -o "$dir/sonara-kokoro-worker"
  ```

  Run these commands from the repository root with a C compiler installed.
  Sonara starts the worker on the first question and keeps its model loaded until
  shutdown or until another voice provider is selected. Switching from Kokoro
  to OpenAI or Azure ends the local practice and frees the worker’s RAM and VRAM.
  Changing the interview language reloads the model. Installations with only
  the CLI still work and show a note about the worker.

  **Optional NVIDIA GPU acceleration.** Install a compatible NVIDIA driver and
  the sherpa-onnx CUDA 12 / cuDNN 9 release (~191 MB). The CUDA 13 toolkit alone
  cannot supply the CUDA 12 library versions this release expects. If the CUDA
  12 runtime libraries are unavailable system-wide, install them under the
  app's sherpa directory with `uv`. This leaves system libraries intact.

  ```sh
  gpu=sherpa-onnx-v1.12.40-cuda-12.x-cudnn-9.x-linux-x64-gpu.tar.bz2
  gh release download v1.12.40 -R k2-fsa/sherpa-onnx -p "$gpu" -D "$dir" --skip-existing
  tar -xjf "$dir/$gpu" -C "$dir"
  uv pip install --target "$dir/cuda-runtime" --link-mode=copy --no-deps \
    'nvidia-cuda-runtime-cu12<13' 'nvidia-cublas-cu12<13' 'nvidia-curand-cu12<11' \
    'nvidia-cufft-cu12>=11,<12' 'nvidia-cudnn-cu12>=9,<10'
  ```

  Sonara prefers CUDA when the GPU release is installed. If CUDA fails, the
  app shows a note and uses the resident CPU model for the rest of that run.
  For libraries in another location, set `SONARA_KOKORO_CUDA_LIBS` to their
  colon-separated library directories in the environment that launches Sonara.

## Development

```sh
wails dev -tags webkit2_41
```

Backend tests: `go test ./...`. Frontend format: `cd frontend && npm run format`.

## Build and install

```sh
./install.sh          # checks dependencies, builds, installs to ~/.local/bin
./install.sh --check  # only lists what is missing
```

It stops with the install command for each missing required dependency, and
lists the optional ones with what you lose without them. It also installs a
desktop entry and the icon.

## Project layout

- `main.go`: only embeds the frontend and the icon and calls `app.Run`.
- `internal/`: packages grouped by feature, each with its own tests.
  - `app`: the Wails façade. `App` and the methods bound to the UI, split by
    feature (`app_listen.go`, `app_sessions.go`, `meeting.go`, `practice.go`…).
    It orchestrates; the packages below do the work.
  - `meeting`: cutting each track into blocks, transcript export, the text the
    speaker-naming prompt reads.
  - `practice`: when an answer starts, ends or talks over the interviewer
    (`ReadAnswer`), the live transcription stream, mic filters and prompts.
  - `storage`: SQLite repository (sessions, settings, transcripts) and migrations.
  - `audio`: the PCM format, levels, ambient noise floor, WebRTC voice detection, WAV.
  - `pipewire`: stream and microphone discovery, and the echo canceller.
  - `diarize`: speaker separation; `Engine` with Gemini and sherpa-onnx strategies.
  - `meetbridge`: the loopback endpoint for the Meet extension and name matching.
  - `logx`, `strutil`, `fsutil`: logging, string and file helpers.
- `backend/`: the in-process assistant API. See [`backend/README.md`](backend/README.md).
- `frontend/`: the Vue UI.
- `extension/meet/`: the Google Meet Chrome extension.
