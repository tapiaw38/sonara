# Backend conventions

Follow `practiq-be/AGENTS.md` architecture:

- One file per public use case and one file per HTTP endpoint.
- Use cases expose one `Execute` method, private implementations, and constructors.
- Handlers bind the use-case input and return its output; keep provider calls in integrations.
- Keep configuration and provider/model routing outside handlers.
- Never log prompts, transcripts, screenshots, audio, or API keys.

The service is local-first. Bind to loopback unless an authenticated deployment is configured.
