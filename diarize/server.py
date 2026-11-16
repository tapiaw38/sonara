#!/usr/bin/env python3
"""Local speaker-diarization service for Sonara's meeting transcription.

Loopback-only HTTP service, stdlib only (no web framework — one endpoint does
not need one). It loads the pyannote.audio pipeline once, in a background
thread, and answers two routes:

  GET  /health   -> 200 once the model has loaded, 503 while loading
  POST /diarize   WAV bytes in the body -> {"speakers": [{"start_ms", "end_ms",
                   "speaker"}, ...]}; the "speaker" labels are the model's own
                   (e.g. "SPEAKER_00") and are NOT stable across calls — the Go
                   side renumbers them per session (see diarize.go).

Started by Sonara itself (see diarize.go's startProcess), one process per app
run, stopped on quit. Run it by hand first to confirm your setup works:

    python3 diarize/server.py

*** UNVERIFIED ***: this file has only been syntax-checked (py_compile). It
was never run against a real model: that needs `pip install -r
requirements.txt` (pulls PyTorch — a large, slow install) and a Hugging Face
access token for the gated model, after accepting its terms on huggingface.co.
Both are the user's own account, not something this assistant can set up or
substitute. Expect to debug real issues here before trusting it on a real
meeting; see README.md in this directory.
"""
from __future__ import annotations

import io
import json
import os
import sys
import tempfile
import threading
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = os.environ.get("PYANNOTE_MODEL", "pyannote/speaker-diarization-community-1")
HOST = os.environ.get("SONARA_DIARIZE_HOST", "127.0.0.1")
PORT = int(os.environ.get("SONARA_DIARIZE_PORT", "8778"))
MAX_BODY_BYTES = 2 * 1024 * 1024 * 1024  # a 3-hour mono 16kHz WAV is ~330MB

_ready = threading.Event()
_pipeline = None
_load_error: Exception | None = None


def _load_pipeline() -> None:
    global _pipeline, _load_error
    try:
        # Imported here, not at module scope, so --help and a syntax check
        # never require torch/pyannote to be installed.
        from pyannote.audio import Pipeline

        token = os.environ.get("HF_TOKEN")
        if not token:
            raise RuntimeError(
                "HF_TOKEN is not set. Accept the model's terms at "
                f"https://huggingface.co/{MODEL} and export a Hugging Face "
                "access token as HF_TOKEN before starting this service."
            )
        _pipeline = Pipeline.from_pretrained(MODEL, use_auth_token=token)
    except Exception as exc:  # noqa: BLE001 - reported via /health, not raised
        _load_error = exc
        print(f"diarize: failed to load {MODEL}: {exc}", file=sys.stderr, flush=True)
    finally:
        _ready.set()


def _diarize_wav(wav_bytes: bytes) -> list[dict]:
    if _pipeline is None:
        raise RuntimeError(str(_load_error) if _load_error else "model not loaded")
    # pyannote.audio wants a file path (or a pre-decoded waveform); a temp file
    # keeps this simple and avoids guessing which torchaudio backend is present.
    with tempfile.NamedTemporaryFile(suffix=".wav") as tmp:
        tmp.write(wav_bytes)
        tmp.flush()
        diarization = _pipeline(tmp.name)
    spans = []
    for turn, _, speaker in diarization.itertracks(yield_label=True):
        spans.append({
            "start_ms": round(turn.start * 1000),
            "end_ms": round(turn.end * 1000),
            "speaker": str(speaker),
        })
    return spans


def _validate_wav(data: bytes) -> None:
    # A cheap sanity check before handing it to the model: catches a client
    # mistake (wrong content, truncated upload) with a clear message instead
    # of an opaque failure three layers into pyannote/torchaudio.
    try:
        with wave.open(io.BytesIO(data), "rb") as handle:
            if handle.getnchannels() < 1 or handle.getnframes() == 0:
                raise ValueError("empty or channel-less WAV")
    except Exception as exc:  # noqa: BLE001
        raise ValueError(f"invalid WAV: {exc}") from exc


class Handler(BaseHTTPRequestHandler):
    # Audio and transcript text are never written to the access log.
    def log_message(self, format: str, *args) -> None:  # noqa: A002
        print("diarize: %s - %s" % (self.address_string(), format % args), file=sys.stderr)

    def _json(self, status: int, payload: dict) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler's own naming
        if self.path != "/health":
            self._json(404, {"error": "not found"})
            return
        if not _ready.is_set():
            self._json(503, {"status": "loading"})
            return
        if _pipeline is None:
            self._json(503, {"status": "failed", "error": str(_load_error)})
            return
        self._json(200, {"status": "ok", "model": MODEL})

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/diarize":
            self._json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0 or length > MAX_BODY_BYTES:
            self._json(400, {"error": f"content length {length} out of range"})
            return
        data = self.rfile.read(length)
        try:
            _validate_wav(data)
            spans = _diarize_wav(data)
        except Exception as exc:  # noqa: BLE001 - every failure must still answer JSON
            self._json(500, {"error": str(exc)})
            return
        self._json(200, {"speakers": spans})


def main() -> None:
    threading.Thread(target=_load_pipeline, daemon=True).start()
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"diarize: listening on {HOST}:{PORT}, loading {MODEL}…", file=sys.stderr, flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
