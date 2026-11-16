# Diarization service (phase 2, experimental)

Separates participants on the meeting's audio-output track after a
transcript recording stops, so segments say "Participante 1" / "Participante
2" instead of a single generic "Participante". Off by default. The
microphone track is never touched by this: "Yo" is decided by which device
captured it, not by analyzing a voice.

**Status: not run end to end in this repository.** `server.py`'s HTTP layer
(health check, request/response shape, WAV validation) was tested with a
stubbed model — see the smoke test in the PR/commit this file shipped with.
The actual pyannote model was never loaded or run here: that needs installing
PyTorch (a large, slow download) and a Hugging Face access token tied to your
own account. Expect to do some of your own debugging the first time you run
this for real.

## Setup

1. Accept the model's terms at
   <https://huggingface.co/pyannote/speaker-diarization-community-1> (requires
   a free Hugging Face account).
2. Create an access token at <https://huggingface.co/settings/tokens> (read
   access is enough) and export it — never put it in a file in this repo:
   ```sh
   export HF_TOKEN=hf_your_token_here
   ```
3. Create a virtualenv and install dependencies (CPU-only PyTorch is enough to
   try this; a GPU is faster but not required):
   ```sh
   cd diarize
   python3 -m venv .venv
   source .venv/bin/activate
   pip install -r requirements.txt
   ```
4. Run the service by hand first, to see real errors before Sonara hides them
   behind its own timeout:
   ```sh
   python3 server.py
   # in another shell:
   curl http://127.0.0.1:8778/health
   ```
   The first `/health` call can take a while: the model loads in the
   background on startup, and `/health` answers 503 until that finishes.

5. Once that works, enable it in Sonara:
   ```sh
   export SONARA_DIARIZE_ENABLED=1
   # Only needed if the venv's python3 isn't the one on PATH:
   export SONARA_DIARIZE_PYTHON=/path/to/diarize/.venv/bin/python3
   ```
   Sonara starts `server.py` itself (via `SONARA_DIARIZE_PYTHON`
   `SONARA_DIARIZE_SCRIPT`, default `diarize/server.py` relative to the
   working directory the app was launched from) the first time a meeting
   recording stops, and keeps it running until the app quits. If it never
   becomes healthy, that meeting's participants just stay unlabeled — nothing
   about recording or transcription depends on this working.

## What this does not do

- No live, per-block diarization: pyannote's speaker labels are not stable
  across independent calls, so this runs once, on the whole accumulated
  meeting_output recording, after capture stops. Participant names only
  appear once that pass finishes (see `diarize.go`'s `runDiarization`).
  Memory for that recording is bounded (`diarizeMaxDuration`, 3 hours) and is
  released as soon as the pass is done, whether it succeeds or not.
- No recognition of *who* a participant is (no names from voice alone) and no
  matching of the meeting's own user inside the mixed output track (echo
  detection) — that is phase 3 in `PLAN_TRANSCRIPCION_REUNIONES.md` and is not
  implemented.
- No GPU requirement, but no GPU support has been exercised either; pyannote
  uses whatever PyTorch finds.
