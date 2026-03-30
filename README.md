# InterviewPilot

Desktop app that assists in live job interviews and technical coding challenges. Captures system audio, transcribes in real-time with Whisper, translates, and generates AI responses based on your CV, documents, and job description context.

## Features

- System audio loopback capture (WASAPI on Windows, PulseAudio/PipeWire on Linux)
- Real-time transcription via faster-whisper
- Automatic translation (deep-translator)
- AI responses with conversation memory (last 20 exchanges)
- Screenshot and 5s screen recording with AI vision for coding challenges
- Document management (PDF, TXT, MD, CSV)
- Job Description context injection
- Session history viewer with date filter
- Window pinning and opacity control
- Screen capture protection (Windows only)
- Two modes: Interview and Daily Communication

## Requirements

- Python 3.10+
- API key for at least one AI provider (Groq, OpenAI, Claude, or Gemini)

### Linux

```bash
pip install -r requirements-linux.txt
```

Requires PulseAudio or PipeWire with a monitor/loopback source enabled.

### Windows

```bash
pip install -r requirements-windows.txt
```

## Setup

1. Copy `.env.example` to `.env` and fill in your API keys:

```env
GROQ_API_KEY=your_key_here
GEMINI_API_KEY=your_key_here
CLAUDE_API_KEY=your_key_here
OPENAI_API_KEY=your_key_here

# Optional: "windows" or "linux" (auto-detected if not set)
OS_PLATFORM=
```

2. Set your preferred AI provider in `config.py`:

```python
AI_PROVIDER = "groq"  # groq | openai | claude | gemini
```

3. Run the app:

```bash
python main.py
```

## AI Providers

| Provider       | Text Model               | Vision Model                   |
| -------------- | ------------------------ | ------------------------------ |
| Groq (default) | llama-3.3-70b-versatile  | llama-4-scout-17b-16e-instruct |
| OpenAI         | gpt-4.1-nano             | gpt-4.1-nano                   |
| Claude         | claude-sonnet-4-20250514 | claude-sonnet-4-20250514       |
| Gemini         | gemini-2.0-flash         | gemini-2.0-flash               |

## Data Storage

User data (documents, history, job description) is stored in:

- **Windows:** `%APPDATA%\InterviewPilot\`
- **Linux:** `~/.local/share/InterviewPilot/`

## Project Structure

```
ai/          # AI providers (Groq, OpenAI, Claude, Gemini) + factory
core/        # Audio capture, transcriber, translator, silence monitor, screen capture, history
documents/   # Document loader (PDF, TXT, MD, CSV)
ui/          # App window, modals, theme, i18n
config.py    # All configuration and constants
main.py      # Entry point
```
