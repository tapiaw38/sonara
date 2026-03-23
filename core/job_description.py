import os

from config import APP_DATA_DIR


JD_FILE = os.path.join(APP_DATA_DIR, "job_description.txt")


class JobDescription:
    """Gestiona la Job Description guardada."""

    @staticmethod
    def load() -> str:
        if os.path.exists(JD_FILE):
            with open(JD_FILE, "r", encoding="utf-8", errors="ignore") as f:
                return f.read().strip()
        return ""

    @staticmethod
    def save(text: str):
        with open(JD_FILE, "w", encoding="utf-8") as f:
            f.write(text.strip())

    @staticmethod
    def exists() -> bool:
        return os.path.exists(JD_FILE) and os.path.getsize(JD_FILE) > 0
