import os
from datetime import datetime

from config import HISTORY_DIR

# Log format labels (shared with history_modal.py for parsing)
LABEL_INTERVIEWER = "INTERVIEWER:"
LABEL_AI_RESPONSE = "AI RESPONSE:"
LABEL_TRANSLATION = "TRADUCCION:"


class ConversationHistory:
    """Guarda el historial de conversaciones en archivos txt."""

    def __init__(self):
        self._history_dir = HISTORY_DIR

        timestamp = datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
        self._filepath = os.path.join(self._history_dir, f"session_{timestamp}.txt")
        self._write_header()

    def _write_header(self):
        with open(self._filepath, "w", encoding="utf-8") as f:
            f.write(f"Session: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n")
            f.write("=" * 60 + "\n\n")

    def log_interviewer(self, text: str, timestamp: str, translation: str | None = None):
        with open(self._filepath, "a", encoding="utf-8") as f:
            f.write(f"[{timestamp}] {LABEL_INTERVIEWER} {text}\n")
            if translation:
                f.write(f"           {LABEL_TRANSLATION} {translation}\n")
            f.write("\n")

    def log_ai_response(self, text: str, timestamp: str, translation: str | None = None):
        with open(self._filepath, "a", encoding="utf-8") as f:
            f.write(f"[{timestamp}] {LABEL_AI_RESPONSE} {text}\n")
            if translation:
                f.write(f"           {LABEL_TRANSLATION} {translation}\n")
            f.write("\n")
