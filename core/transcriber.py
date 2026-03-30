import queue
from dataclasses import dataclass
from datetime import datetime

import numpy as np
from faster_whisper import WhisperModel

from config import WHISPER_MODEL, WHISPER_COMPUTE, SILENCE_THRESHOLD


@dataclass
class TranscriptionResult:
    text: str
    language: str
    timestamp: str


class Transcriber:
    """Transcribe segmentos de audio usando Whisper."""

    def __init__(self):
        self._model: WhisperModel | None = None

    def load_model(self):
        self._model = WhisperModel(WHISPER_MODEL, device="cpu", compute_type=WHISPER_COMPUTE)

    def transcribe(self, audio: np.ndarray) -> TranscriptionResult | None:
        """Transcribe audio. Retorna None si es silencio o no hay texto."""
        if np.abs(audio).mean() < SILENCE_THRESHOLD:
            return None

        segments, info = self._model.transcribe(audio, beam_size=3, vad_filter=True)
        text_parts = [seg.text.strip() for seg in segments if seg.text.strip()]

        if not text_parts:
            return None

        return TranscriptionResult(
            text=" ".join(text_parts),
            language=info.language,
            timestamp=datetime.now().strftime("%H:%M:%S"),
        )
