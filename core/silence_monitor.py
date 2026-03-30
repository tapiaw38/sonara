import time
import threading
from dataclasses import dataclass
from datetime import datetime

from ai.base import AIProvider
from core.translator import TextTranslator
from config import SILENCE_TIMEOUT


@dataclass
class ConversationEntry:
    text: str
    language: str


class SilenceMonitor:
    """Detecta silencios prolongados y genera respuestas con IA con memoria conversacional."""

    def __init__(self, ai_provider: AIProvider, translator: TextTranslator, on_event):
        self._ai = ai_provider
        self._translator = translator
        self._on_event = on_event

        self._buffer: list[ConversationEntry] = []
        self._lock = threading.Lock()
        self._last_speech_time = time.time()
        self._answered_up_to = 0
        self._running = False

        # Historial de chat para mantener contexto
        self._chat_history: list[dict] = []

    @property
    def chat_history(self) -> list[dict]:
        return self._chat_history

    def record_speech(self, text: str, language: str):
        with self._lock:
            self._buffer.append(ConversationEntry(text, language))
        self._last_speech_time = time.time()

    def run(self):
        self._running = True
        while self._running:
            time.sleep(1)
            self._check_silence()

    def stop(self):
        self._running = False

    def _check_silence(self):
        with self._lock:
            buffer_len = len(self._buffer)

        if buffer_len == 0 or buffer_len == self._answered_up_to:
            return

        elapsed = time.time() - self._last_speech_time
        if elapsed < SILENCE_TIMEOUT:
            return

        with self._lock:
            new_entries = self._buffer[self._answered_up_to:]
            self._answered_up_to = buffer_len

        self._generate_response(new_entries)

    def _generate_response(self, entries: list[ConversationEntry]):
        full_text = " ".join(e.text for e in entries)
        detected_lang = entries[-1].language
        timestamp = datetime.now().strftime("%H:%M:%S")

        self._on_event("ai_thinking", "", "")

        # Enviar pregunta con historial
        answer = self._ai.ask(full_text, detected_lang, self._chat_history)

        # Guardar en historial para mantener contexto
        self._chat_history.append({"role": "user", "content": full_text})
        self._chat_history.append({"role": "assistant", "content": answer})

        self._on_event("ai", answer, timestamp)

        if self._translator.needs_translation(detected_lang):
            translated = self._translator.translate(answer)
            if translated:
                self._on_event("ai_translation", translated, timestamp)

        self._on_event("status_restore", "", "")
