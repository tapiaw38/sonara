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
from core.history import ConversationHistory
from core.job_description import JobDescription
from documents.loader import DocumentLoader
from ui.capture_guard import CaptureGuard
from ui.job_modal import JobDescriptionModal
from ui.history_modal import HistoryModal
from ui.docs_modal import DocsModal

# ─── Theme ───────────────────────────────────────────────────
BG_PRIMARY = "#0c0c14"
BG_HEADER = "#12121e"
BG_PANEL = "#0e0e1a"
BG_PANEL_HEADER = "#16162a"
BG_TEXT = "#0a0a12"
ACCENT = "#3b82f6"
ACCENT_HOVER = "#2563eb"
ACCENT_WARM = "#f59e0b"
ACCENT_WARM_HOVER = "#d97706"
TEXT_PRIMARY = "#e2e8f0"
TEXT_SECONDARY = "#94a3b8"
TEXT_MUTED = "#475569"
TEXT_TRANSLATION = "#60a5fa"
DANGER = "#ef4444"
SURFACE = "#1e1e2e"
BORDER = "#1e293b"


class TranslatorApp(ctk.CTk):

    def __init__(self):
        super().__init__()

        self._ui_queue: queue.Queue = queue.Queue()
        self._audio_queue: queue.Queue = queue.Queue()
        self._running = True
        self._capture_guard: CaptureGuard | None = None
        self._ai_provider = None
        self._history = ConversationHistory()

        self._configure_window()
        self._build_ui()
        self._schedule_capture_guard()
        self._start_backend()
        self._poll_ui_queue()

    # ─── Window Setup ────────────────────────────────────────

    def _configure_window(self):
        self.title(WINDOW_TITLE)
        self.geometry(WINDOW_SIZE)
        self.configure(fg_color=BG_PRIMARY)
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
        self._build_toolbar()
        self._build_panels()
        self._build_statusbar()

    def _build_header(self):
        header = ctk.CTkFrame(self, fg_color=BG_HEADER, height=48, corner_radius=0)
        header.pack(fill="x")
        header.pack_propagate(False)

        # Logo / Title
        title_frame = ctk.CTkFrame(header, fg_color="transparent")
        title_frame.pack(side="left", padx=16)

        ctk.CTkLabel(
            title_frame, text="AUDIO TRANSLATOR",
            font=ctk.CTkFont(family="Segoe UI", size=15, weight="bold"),
            text_color=TEXT_PRIMARY,
        ).pack(side="left")

        # Provider badge
        ctk.CTkLabel(
            title_frame, text=f"  {AI_PROVIDER.upper()}",
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        ).pack(side="left", padx=(8, 0))

        # REC indicator
        self._rec_frame = ctk.CTkFrame(header, fg_color="transparent")
        self._rec_frame.pack(side="left", padx=(20, 0))

        ctk.CTkLabel(
            self._rec_frame, text="●",
            font=ctk.CTkFont(size=10), text_color=DANGER,
        ).pack(side="left")

        ctk.CTkLabel(
            self._rec_frame, text=" REC",
            font=ctk.CTkFont(family="Segoe UI", size=10, weight="bold"),
            text_color=DANGER,
        ).pack(side="left")

    def _build_toolbar(self):
        toolbar = ctk.CTkFrame(self, fg_color=BG_HEADER, height=36, corner_radius=0)
        toolbar.pack(fill="x")
        toolbar.pack_propagate(False)

        # Separator line
        sep = ctk.CTkFrame(toolbar, fg_color=BORDER, height=1)
        sep.place(relx=0, rely=0, relwidth=1)

        # ─── Left: Opacity control ──────────────────
        left = ctk.CTkFrame(toolbar, fg_color="transparent")
        left.pack(side="left", padx=16)

        ctk.CTkLabel(
            left, text="Opacidad",
            font=ctk.CTkFont(family="Segoe UI", size=11),
            text_color=TEXT_SECONDARY,
        ).pack(side="left", padx=(0, 8))

        self._opacity_slider = ctk.CTkSlider(
            left, from_=0.3, to=1.0, number_of_steps=14,
            width=100, height=14,
            progress_color=ACCENT, button_color=TEXT_PRIMARY,
            button_hover_color="#ffffff", fg_color=SURFACE,
            corner_radius=7, button_corner_radius=7,
            command=self._change_opacity,
        )
        self._opacity_slider.set(WINDOW_OPACITY)
        self._opacity_slider.pack(side="left")

        # ─── Right: Controls ────────────────────────
        right = ctk.CTkFrame(toolbar, fg_color="transparent")
        right.pack(side="right", padx=16)

        # Screen capture switch
        self._hide_var = ctk.BooleanVar(value=True)
        self._hide_switch = ctk.CTkSwitch(
            right, text="Oculta", variable=self._hide_var,
            command=self._toggle_capture,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            text_color=TEXT_SECONDARY,
            progress_color=ACCENT, button_color=TEXT_PRIMARY,
            button_hover_color="#ffffff", fg_color=SURFACE,
            width=36, height=18,
        )
        self._hide_switch.pack(side="right", padx=(12, 0))

        ctk.CTkLabel(
            right, text="Screen",
            font=ctk.CTkFont(family="Segoe UI", size=11),
            text_color=TEXT_SECONDARY,
        ).pack(side="right")

        # Separator
        ctk.CTkFrame(right, fg_color=BORDER, width=1, height=20).pack(side="right", padx=12)

        # Pin button
        self._pinned = True
        self._pin_btn = ctk.CTkButton(
            right, text="Pin", width=50, height=26,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=ACCENT, hover_color=ACCENT_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=self._toggle_pin,
        )
        self._pin_btn.pack(side="right")

        # Separator
        ctk.CTkFrame(right, fg_color=BORDER, width=1, height=20).pack(side="right", padx=12)

        # History button
        ctk.CTkButton(
            right, text="Historial", width=70, height=26,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=SURFACE, hover_color=ACCENT_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=self._open_history_modal,
        ).pack(side="right")

        # Separator
        ctk.CTkFrame(right, fg_color=BORDER, width=1, height=20).pack(side="right", padx=12)

        # Docs button
        self._docs_btn = ctk.CTkButton(
            right, text="Docs", width=55, height=26,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=SURFACE, hover_color=ACCENT_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=self._open_docs_modal,
        )
        self._docs_btn.pack(side="right")

        # Separator
        ctk.CTkFrame(right, fg_color=BORDER, width=1, height=20).pack(side="right", padx=12)

        # Job Description button
        jd_color = "#22c55e" if JobDescription.exists() else SURFACE
        self._jd_btn = ctk.CTkButton(
            right, text="Job Desc", width=70, height=26,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=jd_color, hover_color=ACCENT_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=self._open_job_modal,
        )
        self._jd_btn.pack(side="right")

    def _build_panels(self):
        content = ctk.CTkFrame(self, fg_color=BG_PRIMARY)
        content.pack(fill="both", expand=True, padx=12, pady=(8, 4))
        content.grid_columnconfigure(0, weight=1)
        content.grid_columnconfigure(1, weight=1)
        content.grid_rowconfigure(0, weight=1)

        self._interviewer_text = self._build_panel(
            content, column=0, title="Interviewer", icon="●", icon_color=ACCENT,
        )
        self._ai_text = self._build_panel(
            content, column=1, title="AI Response", icon="●", icon_color=ACCENT_WARM,
        )

    def _build_panel(self, parent, column: int, title: str, icon: str, icon_color: str) -> ctk.CTkTextbox:
        frame = ctk.CTkFrame(parent, fg_color=BG_PANEL, corner_radius=12, border_width=1, border_color=BORDER)
        padx = (0, 4) if column == 0 else (4, 0)
        frame.grid(row=0, column=column, sticky="nsew", padx=padx)

        # Panel header
        panel_header = ctk.CTkFrame(frame, fg_color=BG_PANEL_HEADER, height=38, corner_radius=12)
        panel_header.pack(fill="x", padx=6, pady=(6, 0))
        panel_header.pack_propagate(False)

        ctk.CTkLabel(
            panel_header, text=f"  {icon}  {title}",
            font=ctk.CTkFont(family="Segoe UI", size=12, weight="bold"),
            text_color=icon_color,
        ).pack(side="left", padx=8, anchor="w")

        # Text area
        textbox = ctk.CTkTextbox(
            frame, fg_color=BG_TEXT, text_color=TEXT_PRIMARY,
            font=ctk.CTkFont(family="Consolas", size=12),
            corner_radius=8, wrap="word", state="disabled",
            border_width=0,
        )
        textbox.pack(fill="both", expand=True, padx=6, pady=6)

        # Text tags
        textbox._textbox.tag_configure("timestamp", foreground=TEXT_MUTED)
        textbox._textbox.tag_configure("original", foreground=TEXT_PRIMARY)
        textbox._textbox.tag_configure("answer", foreground=ACCENT_WARM)
        textbox._textbox.tag_configure("translation", foreground=TEXT_TRANSLATION)
        textbox._textbox.tag_configure("thinking", foreground=TEXT_SECONDARY)

        return textbox

    def _build_statusbar(self):
        statusbar = ctk.CTkFrame(self, fg_color=BG_HEADER, height=28, corner_radius=0)
        statusbar.pack(fill="x", side="bottom")
        statusbar.pack_propagate(False)

        # Separator
        sep = ctk.CTkFrame(statusbar, fg_color=BORDER, height=1)
        sep.place(relx=0, rely=0, relwidth=1)

        self._status_label = ctk.CTkLabel(
            statusbar, text="Iniciando...",
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        )
        self._status_label.pack(side="left", padx=16)

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

    def _toggle_pin(self):
        self._pinned = not self._pinned
        self.attributes("-topmost", self._pinned)
        if self._pinned:
            self._pin_btn.configure(fg_color=ACCENT, text="Pin")
        else:
            self._pin_btn.configure(fg_color=SURFACE, text="Pin")

    def _change_opacity(self, value):
        self.attributes("-alpha", value)

    def _open_job_modal(self):
        current = JobDescription.load()
        JobDescriptionModal(self, current, self._save_job_description)

    def _open_docs_modal(self):
        DocsModal(self, self._reload_docs)

    def _reload_docs(self):
        """Recarga documentos y actualiza el contexto del AI provider."""
        loader = DocumentLoader(DOCS_FOLDER)
        docs_context, files = loader.load_all()
        if self._ai_provider:
            self._ai_provider.set_context(docs_context)
        has_docs = len(files) > 0
        self._docs_btn.configure(fg_color="#22c55e" if has_docs else SURFACE)
        self._set_status(f"Docs: {', '.join(files)}" if files else "Sin documentos")

    def _open_history_modal(self):
        HistoryModal(self)

    def _save_job_description(self, text: str):
        JobDescription.save(text)
        if self._ai_provider:
            self._ai_provider.set_job_description(text)
        has_jd = bool(text.strip())
        self._jd_btn.configure(fg_color="#22c55e" if has_jd else SURFACE)
        self._set_status("Job Description guardada" if has_jd else "Job Description eliminada")

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
            "docs_loaded": lambda: self._docs_btn.configure(
                fg_color="#22c55e" if int(text) > 0 else SURFACE),
        }

        handler = handlers.get(event_type)
        if handler:
            handler()

        # Log to history
        if event_type == "interviewer":
            self._history.log_interviewer(text, timestamp)
        elif event_type == "ai":
            self._history.log_ai_response(text, timestamp)
        elif event_type == "interviewer_translation":
            self._history.log_interviewer(text, timestamp)
        elif event_type == "ai_translation":
            self._history.log_ai_response(text, timestamp)

    # ─── Backend ─────────────────────────────────────────────

    def _start_backend(self):
        threading.Thread(target=self._backend_init, daemon=True).start()

    def _backend_init(self):
        # Documentos
        self._emit("status", "Cargando documentos...")
        loader = DocumentLoader(DOCS_FOLDER)
        docs_context, files = loader.load_all()
        self._emit("docs_loaded", str(len(files)), "")
        if files:
            self._emit("status", f"Docs: {', '.join(files)}")

        # AI Provider
        ai_provider = create_ai_provider(AI_PROVIDER)
        ai_provider.set_context(docs_context)
        ai_provider.set_job_description(JobDescription.load())
        self._ai_provider = ai_provider

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
