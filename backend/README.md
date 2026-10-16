# Sonara backend

Local-first Go API for Sonara's interview assistant. It follows the use-case, handler, integration, and configuration separation used by `practiq-be`.

## Run

The Wails app initializes this backend in-process; no separate API process is needed for desktop controls. To run the HTTP API independently from repository root:

```sh
export OPENAI_API_KEY="..."
go run ./backend/cmd/api
```

Service listens on `127.0.0.1:8777`. Copy `.env.example` values into the process environment to configure model tiers. Never put `OPENAI_API_KEY` in frontend code or commit it.

## Endpoints

- `GET /health`
- `POST /v1/assist/chat` with `{"message":"...","mode":"interview|daily|coding|coaching","context":"..."}`
- `POST /v1/assist/screen` with `{"image_data":"data:image/png;base64,...","prompt":"..."}`
- `POST /v1/assist/transcription` multipart fields `file` and optional `language` for completed audio segments

The chat router selects one task agent per request: interview/coaching uses the fast model, code tasks use reasoning, and system design uses the expert model. Screen analysis uses reasoning. Transcription uses its dedicated audio model. Each tier is configurable; requests do not make extra classifier calls.

The API binds only to loopback, checks browser `Origin` and `Host`, and rejects non-local connections. Set `SONARA_ALLOWED_ORIGINS` to the exact local UI origins when needed. This transcribes completed audio segments; a persistent Realtime session is a separate integration. Before remote deployment, add Sonara account authentication, per-user quotas, and request rate limits; do not change the bind address alone.
