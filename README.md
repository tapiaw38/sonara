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

- Go, Node.js and the [Wails CLI](https://wails.io/docs/gettingstarted/installation)
- WebKitGTK 4.1
- PipeWire (`pw-record`, `pw-dump`) for audio capture, `ffmpeg`
- `grim` for screen capture, `wlrctl` for window focus
- An OpenAI API key (set in the app). DeepSeek and Gemini keys are optional:
  Gemini enables cloud speaker separation.

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
