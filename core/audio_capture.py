import queue

import numpy as np

from config import SEGMENT_SECONDS, AUDIO_FRAMES_BUFFER, IS_WINDOWS

if IS_WINDOWS:
    import pyaudiowpatch as pyaudio
else:
    import sounddevice as sd


class AudioCapture:
    """Captura audio de salida del sistema via loopback (WASAPI en Windows, PulseAudio/PipeWire en Linux)."""

    TARGET_SAMPLE_RATE = 16000

    def __init__(self, audio_queue: queue.Queue):
        self._audio_queue = audio_queue
        self._running = False
        self._pyaudio = None
        self._loopback: dict | None = None
        self._device_name: str = ""

    def initialize(self) -> str:
        """Busca el dispositivo loopback. Retorna nombre del dispositivo."""
        if IS_WINDOWS:
            return self._initialize_windows()
        return self._initialize_linux()

    def _initialize_windows(self) -> str:
        self._pyaudio = pyaudio.PyAudio()
        wasapi = self._pyaudio.get_host_api_info_by_type(pyaudio.paWASAPI)
        default_out = self._pyaudio.get_device_info_by_index(wasapi["defaultOutputDevice"])

        for i in range(self._pyaudio.get_device_count()):
            dev = self._pyaudio.get_device_info_by_index(i)
            if dev["name"].startswith(default_out["name"]) and dev.get("isLoopbackDevice"):
                self._loopback = dev
                return dev["name"]

        raise RuntimeError("No se encontró dispositivo loopback WASAPI")

    def _initialize_linux(self) -> str:
        devices = sd.query_devices()
        for dev in devices:
            name = dev["name"].lower()
            if "monitor" in name or "loopback" in name:
                self._device_name = dev["name"]
                return dev["name"]

        default = sd.query_devices(kind="input")
        self._device_name = default["name"]
        return self._device_name

    def run(self):
        """Loop principal de captura (ejecutar en un hilo)."""
        if IS_WINDOWS:
            self._run_windows()
        else:
            self._run_linux()

    def _run_windows(self):
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
                raw_frames = self._record_segment_windows(stream, frames_per_segment)
                if raw_frames:
                    audio = self._process_audio(raw_frames, channels, rate)
                    self._audio_queue.put(audio)
        finally:
            stream.stop_stream()
            stream.close()

    def _run_linux(self):
        rate = self.TARGET_SAMPLE_RATE
        frames_per_segment = rate * SEGMENT_SECONDS

        self._running = True
        with sd.InputStream(
            device=self._device_name or None,
            samplerate=rate,
            channels=1,
            dtype="float32",
            blocksize=AUDIO_FRAMES_BUFFER,
        ) as stream:
            while self._running:
                frames = []
                collected = 0
                while collected < frames_per_segment and self._running:
                    data, _ = stream.read(AUDIO_FRAMES_BUFFER)
                    frames.append(data[:, 0])
                    collected += len(data)
                if frames:
                    audio = np.concatenate(frames)
                    self._audio_queue.put(audio)

    def stop(self):
        self._running = False
        if IS_WINDOWS and self._pyaudio:
            self._pyaudio.terminate()

    def _record_segment_windows(self, stream, frames_count: int) -> list[np.ndarray]:
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
