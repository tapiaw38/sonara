# InterviewPilot

## What is this?
Desktop app (Python/CustomTkinter) that assists in live job interviews and technical coding challenges. Captures system audio, transcribes with Whisper, translates in real-time, and generates AI responses based on loaded documents (CV, PDFs) and Job Description context.

## Architecture
- **Pattern**: Strategy + Factory for AI providers, Clean Architecture with separated layers
- **SOLID**: SRP per module, OCP for new providers, DIP via abstract base
- `ai/` — base.py (abstract), groq/openai/claude/gemini providers, factory.py
- `core/` — audio_capture.py, transcriber.py, translator.py, silence_monitor.py, screen_capture.py, history.py, job_description.py
- `documents/` — loader.py (PDF, TXT, MD, CSV)
- `ui/` — app.py, job_modal.py, docs_modal.py, history_modal.py, capture_guard.py

## Key Features
- WASAPI loopback audio capture (pyaudiowpatch)
- Real-time transcription (faster-whisper) + translation (deep-translator)
- AI responses with conversation memory (last 20 exchanges)
- Screenshot + 5s screen recording with AI vision for coding challenges
- Document management (upload/delete via modal)
- Job Description context injection
- Session history viewer with date filter
- Screen capture protection (SetWindowDisplayAffinity)
- Window pinning, opacity control

## AI Providers
- **Groq** (default): llama-3.3-70b-versatile (text), meta-llama/llama-4-scout-17b-16e-instruct (vision, max 5 images)
- **OpenAI**: gpt-4.1-nano
- **Claude**: claude-sonnet-4-20250514
- **Gemini**: gemini-2.0-flash
- Switch via `AI_PROVIDER` in config.py

## Data Storage
User data in `%APPDATA%/InterviewPilot/` (docs/, history/, job_description.txt)

## UI Rules
- Dark theme (#0c0c14 primary), CustomTkinter
- Modals: withdraw → build UI → deiconify (no white flash)
- Action buttons: solid color background, white text (no transparent icons)
- No emojis in labels, use descriptive text
- Keep interface clean and minimal

## AI Response Rules
- Interview mode: max_tokens=256, first person, natural speech, no markdown
- Vision/coding mode: max_tokens=2048, full solution with code
- Never refuse to answer, use general knowledge as fallback
- Maintain conversation context across questions

## Running
```bash
python main.py
```

## Git History
1. feat: initial release of Audio Translator
2. refine: improve interview prompt and limit response length
3. ui: redesign interface with modern UX standards
4. feat: add job description context, conversation memory, and session history
5. feat: add docs manager, history viewer, and migrate user data to AppData
6. feat: add screenshot and screen recording with AI vision analysis
