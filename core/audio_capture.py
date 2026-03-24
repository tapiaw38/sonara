import queue

import numpy as np
import pyaudiowpatch as pyaudio

from config import SEGMENT_SECONDS, AUDIO_FRAMES_BUFFER


class AudioCapture:
    """Captura audio de salida del sistema via WASAPI loopback."""

    TARGET_SAMPLE_RATE = 16000

    def __init__(self, audio_queue: queue.Queue):
        self._audio_queue = audio_queue
        self._running = False
        self._pyaudio: pyaudio.PyAudio | None = None
        self._loopback: dict | None = None

    def initialize(self) -> str:
        """Busca el dispositivo loopback. Retorna nombre del dispositivo."""
        self._pyaudio = pyaudio.PyAudio()

        wasapi = self._pyaudio.get_host_api_info_by_type(pyaudio.paWASAPI)
        default_out = self._pyaudio.get_device_info_by_index(wasapi["defaultOutputDevice"])

        for i in range(self._pyaudio.get_device_count()):
            dev = self._pyaudio.get_device_info_by_index(i)
            if dev["name"].startswith(default_out["name"]) and dev.get("isLoopbackDevice"):
                self._loopback = dev
                return dev["name"]

        raise RuntimeError("No se encontró dispositivo loopback WASAPI")

    def run(self):
        """Loop principal de captura (ejecutar en un hilo)."""
        if not self._loopback:
            raise RuntimeError("Llama a initialize() primero")

        rate = int(self._loopback["defaultSampleRate"])
        channels = int(self._loopback["maxInputChannels"])
        frames_per_segment = rate * SEGMENT_SECONDS

        stream = self._pyaudio.open(
            format=pyaudio.paFloat32,
            channels=channels,
            rate=rate,
            input=True,
            input_device_index=self._loopback["index"],
            frames_per_buffer=AUDIO_FRAMES_BUFFER,
        )

        self._running = True
        try:
            while self._running:
                raw_frames = self._record_segment(stream, frames_per_segment)
                if raw_frames:
                    audio = self._process_audio(raw_frames, channels, rate)
                    self._audio_queue.put(audio)
        finally:
            stream.stop_stream()
            stream.close()

    def stop(self):
        self._running = False
        if self._pyaudio:
            self._pyaudio.terminate()

    def _record_segment(self, stream, frames_count: int) -> list[np.ndarray]:
        frames = []
        for _ in range(0, frames_count, AUDIO_FRAMES_BUFFER):
            if not self._running:
                break
            data = stream.read(AUDIO_FRAMES_BUFFER, exception_on_overflow=False)
            frames.append(np.frombuffer(data, dtype=np.float32))
        return frames

    def _process_audio(self, frames: list[np.ndarray], channels: int, rate: int) -> np.ndarray:
        audio = np.concatenate(frames)

        if channels > 1:
            audio = audio.reshape(-1, channels).mean(axis=1)

        if rate != self.TARGET_SAMPLE_RATE:
            length = int(len(audio) * self.TARGET_SAMPLE_RATE / rate)
            audio = np.interp(
                np.linspace(0, len(audio), length),
                np.arange(len(audio)),
                audio,
            ).astype(np.float32)

        return audio
