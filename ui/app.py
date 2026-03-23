import queue
import threading

import customtkinter as ctk

from config import (
    WINDOW_TITLE, WINDOW_SIZE, WINDOW_MIN_SIZE, WINDOW_OPACITY,
    TARGET_LANG, AI_PROVIDER, DOCS_FOLDER,
)
from ai import create_ai_provider
from core.audio_capture import AudioCapture
from core.transcriber import Transcriber
from core.translator import TextTranslator
from core.silence_monitor import SilenceMonitor
from documents.loader import DocumentLoader
from ui.capture_guard import CaptureGuard


class TranslatorApp(ctk.CTk):

    def __init__(self):
        super().__init__()

        self._ui_queue: queue.Queue = queue.Queue()
        self._audio_queue: queue.Queue = queue.Queue()
        self._running = True
        self._capture_guard: CaptureGuard | None = None

        self._configure_window()
        self._build_ui()
        self._schedule_capture_guard()
        self._start_backend()
        self._poll_ui_queue()

    # ─── Window Setup ────────────────────────────────────────

    def _configure_window(self):
        self.title(WINDOW_TITLE)
        self.geometry(WINDOW_SIZE)
        self.configure(fg_color="#0d0d0d")
        self.attributes("-topmost", True)
        self.attributes("-alpha", WINDOW_OPACITY)
        self.minsize(*WINDOW_MIN_SIZE)
        self.protocol("WM_DELETE_WINDOW", self._on_closing)

    def _schedule_capture_guard(self):
        self.after(500, self._init_capture_guard)

    def _init_capture_guard(self):
        try:
            self._capture_guard = CaptureGuard(self)
            self._capture_guard.set_hidden(True)
            self._update_capture_label()
        except Exception:
            self._set_status("No se pudo ocultar ventana")

    # ─── UI Build ────────────────────────────────────────────

    def _build_ui(self):
        self._build_header()
        self._build_panels()

    def _build_header(self):
        header = ctk.CTkFrame(self, fg_color="#1a1a2e", height=45, corner_radius=0)
        header.pack(fill="x")
        header.pack_propagate(False)

        ctk.CTkLabel(
            header, text=f"  AUDIO TRANSLATOR  |  {AI_PROVIDER.upper()}",
            font=ctk.CTkFont(size=14, weight="bold"),
            text_color="#00d4ff",
        ).pack(side="left", padx=15)

        # Status
        self._status_label = ctk.CTkLabel(
            header, text="Iniciando...",
            font=ctk.CTkFont(size=11), text_color="#888888",
        )
        self._status_label.pack(side="right", padx=15)

        # Switch ocultación
        self._hide_var = ctk.BooleanVar(value=True)
        self._hide_switch = ctk.CTkSwitch(
            header, text="Oculta", variable=self._hide_var,
            command=self._toggle_capture,
            font=ctk.CTkFont(size=11), text_color="#cccccc",
            progress_color="#00d4ff", button_color="#ffffff",
            button_hover_color="#dddddd", fg_color="#333333",
            width=40, height=20,
        )
        self._hide_switch.pack(side="right", padx=10)

        # REC indicator
        ctk.CTkLabel(
            header, text="● REC",
            font=ctk.CTkFont(size=11, weight="bold"), text_color="#ff4444",
        ).pack(side="right", padx=5)

    def _build_panels(self):
        content = ctk.CTkFrame(self, fg_color="#0d0d0d")
        content.pack(fill="both", expand=True, padx=10, pady=(5, 10))
        content.grid_columnconfigure(0, weight=1)
        content.grid_columnconfigure(1, weight=1)
        content.grid_rowconfigure(0, weight=1)

        self._interviewer_text = self._build_panel(
            content, column=0, title="  🎙  Interviewer", title_color="#4a9eff",
        )
        self._ai_text = self._build_panel(
            content, column=1, title="  🤖  AI Response", title_color="#ffb347",
        )

    def _build_panel(self, parent, column: int, title: str, title_color: str) -> ctk.CTkTextbox:
        frame = ctk.CTkFrame(parent, fg_color="#111122", corner_radius=10)
        padx = (0, 5) if column == 0 else (5, 0)
        frame.grid(row=0, column=column, sticky="nsew", padx=padx)

        header = ctk.CTkFrame(frame, fg_color="#1a1a3e", height=35, corner_radius=10)
        header.pack(fill="x", padx=5, pady=5)
        header.pack_propagate(False)

        ctk.CTkLabel(
            header, text=title,
            font=ctk.CTkFont(size=13, weight="bold"), text_color=title_color,
        ).pack(side="left", padx=10, anchor="w")

        textbox = ctk.CTkTextbox(
            frame, fg_color="#0a0a1a", text_color="#e0e0e0",
            font=ctk.CTkFont(size=13), corner_radius=8,
            wrap="word", state="disabled",
        )
        textbox.pack(fill="both", expand=True, padx=5, pady=(0, 5))

        textbox._textbox.tag_configure("timestamp", foreground="#555555")
        textbox._textbox.tag_configure("original", foreground="#e0e0e0")
        textbox._textbox.tag_configure("answer", foreground="#ffb347")
        textbox._textbox.tag_configure("translation", foreground="#4a9eff")
        textbox._textbox.tag_configure("thinking", foreground="#888888")

        return textbox

    # ─── UI Helpers ──────────────────────────────────────────

    def _append_to(self, textbox: ctk.CTkTextbox, text: str, timestamp: str, tag: str):
        textbox.configure(state="normal")
        if timestamp:
            textbox._textbox.insert("end", f"{timestamp}  ", "timestamp")
        textbox._textbox.insert("end", f"{text}\n", tag)
        textbox._textbox.see("end")
        textbox.configure(state="disabled")

    def _set_status(self, text: str):
        self._status_label.configure(text=text)

    def _toggle_capture(self):
        if self._capture_guard:
            self._capture_guard.set_hidden(self._hide_var.get())
            self._update_capture_label()

    def _update_capture_label(self):
        if self._capture_guard:
            label = "Oculta" if self._capture_guard.is_hidden else "Visible"
            self._hide_switch.configure(text=label)
            self._set_status(f"Ventana {label.upper()} ante screen share")

    # ─── Event Queue ─────────────────────────────────────────

    def _emit(self, event_type: str, text: str, timestamp: str = ""):
        self._ui_queue.put((event_type, text, timestamp))

    def _poll_ui_queue(self):
        try:
            while True:
                event_type, text, timestamp = self._ui_queue.get_nowait()
                self._handle_event(event_type, text, timestamp)
        except queue.Empty:
            pass

        if self._running:
            self.after(100, self._poll_ui_queue)

    def _handle_event(self, event_type: str, text: str, timestamp: str):
        handlers = {
            "interviewer": lambda: self._append_to(
                self._interviewer_text, text, timestamp, "original"),
            "interviewer_translation": lambda: self._append_to(
                self._interviewer_text, f"  ↳ {text}", timestamp, "translation"),
            "ai_thinking": lambda: self._append_to(
                self._ai_text, "⏳ " + text, "", "thinking"),
            "ai": lambda: self._append_to(
                self._ai_text, text + "\n", timestamp, "answer"),
            "ai_translation": lambda: self._append_to(
                self._ai_text, f"↳ {text}\n", timestamp, "translation"),
            "status": lambda: self._set_status(text),
        }

        handler = handlers.get(event_type)
        if handler:
            handler()

    # ─── Backend ─────────────────────────────────────────────

    def _start_backend(self):
        threading.Thread(target=self._backend_init, daemon=True).start()

    def _backend_init(self):
        # Documentos
        self._emit("status", "Cargando documentos...")
        loader = DocumentLoader(DOCS_FOLDER)
        docs_context, files = loader.load_all()
        if files:
            self._emit("status", f"Docs: {', '.join(files)}")

        # AI Provider
        ai_provider = create_ai_provider(AI_PROVIDER)
        ai_provider.set_context(docs_context)

        # Whisper
        self._emit("status", "Cargando Whisper...")
        transcriber = Transcriber()
        transcriber.load_model()

        # Translator
        translator = TextTranslator(TARGET_LANG)

        # Silence Monitor
        silence_monitor = SilenceMonitor(ai_provider, translator, self._emit)

        # Audio Capture
        self._emit("status", "Buscando audio...")
        audio_capture = AudioCapture(self._audio_queue)
        try:
            device_name = audio_capture.initialize()
            self._emit("status", f"Escuchando: {device_name[:30]}...")
        except RuntimeError as e:
            self._emit("status", f"ERROR: {e}")
            return

        # Audio processing loop
        def process_loop():
            while self._running:
                try:
                    audio = self._audio_queue.get(timeout=1)
                except queue.Empty:
                    continue

                result = transcriber.transcribe(audio)
                if not result:
                    continue

                silence_monitor.record_speech(result.text, result.language)
                self._emit("interviewer", result.text, result.timestamp)

                if translator.needs_translation(result.language):
                    translated = translator.translate(result.text)
                    if translated:
                        self._emit("interviewer_translation", translated, result.timestamp)

        # Launch threads
        threads = [
            threading.Thread(target=audio_capture.run, daemon=True),
            threading.Thread(target=process_loop, daemon=True),
            threading.Thread(target=silence_monitor.run, daemon=True),
        ]
        for t in threads:
            t.start()

    def _on_closing(self):
        self._running = False
        self.destroy()
