import os

from dotenv import load_dotenv

load_dotenv(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"))

# ─── Audio ───────────────────────────────────────────────────
TARGET_LANG = "es"
WHISPER_MODEL = "base"
WHISPER_COMPUTE = "int8"
SEGMENT_SECONDS = 5
SILENCE_THRESHOLD = 0.01
SILENCE_TIMEOUT = 5

# ─── AI Provider ─────────────────────────────────────────────
# Opciones: "gemini", "claude", "openai", "groq"
AI_PROVIDER = "groq"

API_KEYS = {
    "gemini": os.getenv("GEMINI_API_KEY", ""),
    "claude": os.getenv("CLAUDE_API_KEY", ""),
    "openai": os.getenv("OPENAI_API_KEY", ""),
    "groq": os.getenv("GROQ_API_KEY", ""),
}

AI_MODELS = {
    "gemini": "gemini-2.0-flash",
    "claude": "claude-sonnet-4-20250514",
    "openai": "gpt-4.1-nano",
    "groq": "llama-3.3-70b-versatile",
}

# ─── App Mode ───────────────────────────────────────────────
MODE_INTERVIEW = "interview"
MODE_DAILY = "daily"
DEFAULT_MODE = MODE_INTERVIEW

# ─── AI Limits ───────────────────────────────────────────────
MAX_TOKENS_TEXT = 256
MAX_TOKENS_VISION = 2048
CHAT_HISTORY_LIMIT = 20
VISION_HISTORY_LIMIT = 10
MAX_VISION_FRAMES = 5

# ─── Paths ───────────────────────────────────────────────────
BASE_DIR = os.path.dirname(os.path.abspath(__file__))

# User data lives in AppData (persists across updates, works with .exe)
APP_DATA_DIR = os.path.join(os.getenv("APPDATA", BASE_DIR), "InterviewPilot")
DOCS_FOLDER = os.path.join(APP_DATA_DIR, "docs")
DOCS_DAILY_FOLDER = os.path.join(APP_DATA_DIR, "docs_daily")
HISTORY_DIR = os.path.join(APP_DATA_DIR, "history")
PROJECT_PATH_FILE = os.path.join(APP_DATA_DIR, "project_path.txt")

# Create folders on startup
os.makedirs(DOCS_FOLDER, exist_ok=True)
os.makedirs(DOCS_DAILY_FOLDER, exist_ok=True)
os.makedirs(HISTORY_DIR, exist_ok=True)

# ─── Timing ──────────────────────────────────────────────────
UI_POLL_INTERVAL_MS = 100
CAPTURE_DELAY_MS = 500
AUDIO_FRAMES_BUFFER = 512

# ─── UI ──────────────────────────────────────────────────────
WINDOW_TITLE = "InterviewPilot"
WINDOW_SIZE = "900x600"
WINDOW_MIN_SIZE = (700, 400)
WINDOW_OPACITY = 0.92

LANG_NAMES = {
    "en": "English", "es": "Spanish", "fr": "French",
    "pt": "Portuguese", "de": "German", "it": "Italian",
    "ja": "Japanese", "ko": "Korean", "zh": "Chinese",
    "ru": "Russian", "ar": "Arabic", "nl": "Dutch",
}
