import os
import queue
import threading
from datetime import datetime

import customtkinter as ctk

from config import (
    WINDOW_TITLE, WINDOW_SIZE, WINDOW_MIN_SIZE, WINDOW_OPACITY,
    TARGET_LANG, AI_PROVIDER, DOCS_FOLDER, DOCS_DAILY_FOLDER,
    UI_POLL_INTERVAL_MS, CAPTURE_DELAY_MS,
    MODE_INTERVIEW, MODE_DAILY, DEFAULT_MODE, PROJECT_PATH_FILE,
)
from ai import create_ai_provider
from core.audio_capture import AudioCapture
from core.transcriber import Transcriber
from core.translator import TextTranslator
from core.silence_monitor import SilenceMonitor
from core.screen_capture import ScreenCapture
from core.history import ConversationHistory
from core.job_description import JobDescription
from documents.loader import DocumentLoader
from core.project_scanner import scan_project
from ui.capture_guard import CaptureGuard
from ui.job_modal import JobDescriptionModal
from ui.history_modal import HistoryModal
from ui.docs_modal import DocsModal
from ui.i18n import t, get_lang, set_lang, on_lang_change, remove_lang_callback
from ui.theme import (
    FONT_FAMILY, FONT_MONO,
    BG_PRIMARY, BG_HEADER, BG_PANEL, BG_TEXT,
    ACCENT, ACCENT_HOVER, ACCENT_WARM,
    TEXT_WHITE, TEXT_PRIMARY, TEXT_SECONDARY, TEXT_MUTED, TEXT_TRANSLATION,
    DANGER, DANGER_HOVER, SUCCESS,
    SURFACE, BORDER, BTN_HOVER, STATE_ACTIVE,
)


class TranslatorApp(ctk.CTk):

    def __init__(self):
        super().__init__()

        self._ui_queue: queue.Queue = queue.Queue()
        self._audio_queue: queue.Queue = queue.Queue()
        self._running = True
        self._capture_guard: CaptureGuard | None = None
        self._ai_provider = None
        self._screen_capture = ScreenCapture()
        self._history = ConversationHistory()
        self._listening_status: str = ""
        self._device_name: str = ""
        self._current_mode: str = DEFAULT_MODE
        self._project_path: str = self._load_project_path()

        self._configure_window()
        self._build_ui()
        self._schedule_capture_guard()
        self._start_backend()
        self._poll_ui_queue()

        on_lang_change(self._on_language_changed)

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
        self.after(CAPTURE_DELAY_MS, self._init_capture_guard)

    def _init_capture_guard(self):
        try:
            self._capture_guard = CaptureGuard(self)
            self._capture_guard.set_hidden(True)
            self._update_capture_label()
        except Exception:
            self._set_status(t("capture_hide_failed"))

    # ─── UI Build ────────────────────────────────────────────

    def _build_ui(self):
        self._build_header()
        self._build_toolbar()
        self._build_panels()
        self._build_project_bar()
        self._build_statusbar()

    def _build_header(self):
        header = ctk.CTkFrame(self, fg_color=BG_HEADER, height=48, corner_radius=0)
        header.pack(fill="x")
        header.pack_propagate(False)

        # Logo / Title
        title_frame = ctk.CTkFrame(header, fg_color="transparent")
        title_frame.pack(side="left", padx=16)

        ctk.CTkLabel(
            title_frame, text="INTERVIEWPILOT",
            font=ctk.CTkFont(family=FONT_FAMILY, size=15, weight="bold"),
            text_color=TEXT_PRIMARY,
        ).pack(side="left")

        # Provider badge
        ctk.CTkLabel(
            title_frame, text=f"  {AI_PROVIDER.upper()}",
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
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
            font=ctk.CTkFont(family=FONT_FAMILY, size=10, weight="bold"),
            text_color=DANGER,
        ).pack(side="left")

        # ─── Header right: Nav buttons + Language toggle ───
        # Language toggle (rightmost)
        lang_frame = ctk.CTkFrame(header, fg_color=SURFACE, corner_radius=6)
        lang_frame.pack(side="right", padx=(8, 16))

        # Job Description button — Interview mode
        has_jd = JobDescription.exists()
        self._jd_btn = ctk.CTkButton(
            header, text=t("job_desc"), width=62, height=26,
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_PRIMARY if has_jd else TEXT_MUTED,
            corner_radius=4,
            border_width=1, border_color=STATE_ACTIVE if has_jd else BORDER,
            command=self._open_job_modal,
        )
        self._jd_btn.pack(side="right", padx=(4, 0))

        # Docs button — Interview mode
        has_docs = DocumentLoader(DOCS_FOLDER).has_documents()
        self._docs_btn = ctk.CTkButton(
            header, text=t("docs"), width=46, height=26,
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_PRIMARY if has_docs else TEXT_MUTED,
            corner_radius=4,
            border_width=1, border_color=STATE_ACTIVE if has_docs else BORDER,
            command=self._open_docs_modal,
        )
        self._docs_btn.pack(side="right", padx=(4, 0))

        # Docs Daily button — Daily mode (hidden by default)
        has_daily_docs = DocumentLoader(DOCS_DAILY_FOLDER).has_documents()
        self._docs_daily_btn = ctk.CTkButton(
            header, text=t("docs_daily"), width=68, height=26,
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_PRIMARY if has_daily_docs else TEXT_MUTED,
            corner_radius=4,
            border_width=1, border_color=STATE_ACTIVE if has_daily_docs else BORDER,
            command=self._open_docs_daily_modal,
        )
        # Hidden by default (interview mode)

        cur = get_lang()
        self._lang_es_btn = ctk.CTkButton(
            lang_frame, text="ES", width=34, height=24,
            font=ctk.CTkFont(family=FONT_FAMILY, size=10, weight="bold"),
            fg_color=ACCENT if cur == "es" else "transparent",
            hover_color=BTN_HOVER,
            text_color=TEXT_WHITE if cur == "es" else TEXT_MUTED,
            corner_radius=4,
            command=lambda: self._set_ui_lang("es"),
        )
        self._lang_es_btn.pack(side="left", padx=(2, 0), pady=2)

        self._lang_en_btn = ctk.CTkButton(
            lang_frame, text="EN", width=34, height=24,
            font=ctk.CTkFont(family=FONT_FAMILY, size=10, weight="bold"),
            fg_color=ACCENT if cur == "en" else "transparent",
            hover_color=BTN_HOVER,
            text_color=TEXT_WHITE if cur == "en" else TEXT_MUTED,
            corner_radius=4,
            command=lambda: self._set_ui_lang("en"),
        )
        self._lang_en_btn.pack(side="left", padx=(0, 2), pady=2)

    def _build_toolbar(self):
        toolbar = ctk.CTkFrame(self, fg_color=BG_HEADER, height=40, corner_radius=0)
        toolbar.pack(fill="x")
        toolbar.pack_propagate(False)

        # Separator line
        ctk.CTkFrame(toolbar, fg_color=BORDER, height=1).place(relx=0, rely=0, relwidth=1)

        # ─── Left: Capture tools ─────────────────────
        left = ctk.CTkFrame(toolbar, fg_color="transparent")
        left.pack(side="left", padx=(16, 0))

        self._opacity_label = ctk.CTkLabel(
            left, text=t("opacity"),
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
            text_color=TEXT_MUTED,
        )
        self._opacity_label.pack(side="left", padx=(0, 6))

        self._opacity_slider = ctk.CTkSlider(
            left, from_=0.3, to=1.0, number_of_steps=14,
            width=90, height=14,
            progress_color=ACCENT, button_color=TEXT_PRIMARY,
            button_hover_color=TEXT_WHITE, fg_color=SURFACE,
            corner_radius=7, button_corner_radius=7,
            command=self._change_opacity,
        )
        self._opacity_slider.set(WINDOW_OPACITY)
        self._opacity_slider.pack(side="left")

        ctk.CTkFrame(left, fg_color=BORDER, width=1, height=20).pack(side="left", padx=10)

        # Screenshot button (ghost)
        self._screenshot_btn = ctk.CTkButton(
            left, text=t("screenshot"), width=78, height=28,
            font=ctk.CTkFont(family=FONT_FAMILY, size=11),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            border_width=1, border_color=BORDER,
            command=self._take_screenshot,
        )
        self._screenshot_btn.pack(side="left", padx=(0, 4))

        # Record button (ghost)
        self._rec_btn = ctk.CTkButton(
            left, text=t("record_5s"), width=78, height=28,
            font=ctk.CTkFont(family=FONT_FAMILY, size=11),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            border_width=1, border_color=BORDER,
            command=self._record_screen,
        )
        self._rec_btn.pack(side="left")

        ctk.CTkFrame(left, fg_color=BORDER, width=1, height=20).pack(side="left", padx=10)

        # Mode switch (Interview / Daily)
        self._mode_interview_btn = ctk.CTkButton(
            left, text=t("mode_interview"), width=68, height=28,
            font=ctk.CTkFont(family=FONT_FAMILY, size=11),
            fg_color=ACCENT, hover_color=ACCENT_HOVER,
            text_color=TEXT_WHITE, corner_radius=6,
            command=lambda: self._switch_mode(MODE_INTERVIEW),
        )
        self._mode_interview_btn.pack(side="left", padx=(0, 4))

        self._mode_daily_btn = ctk.CTkButton(
            left, text=t("mode_daily"), width=52, height=28,
            font=ctk.CTkFont(family=FONT_FAMILY, size=11),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            border_width=1, border_color=BORDER,
            command=lambda: self._switch_mode(MODE_DAILY),
        )
        self._mode_daily_btn.pack(side="left")

        ctk.CTkFrame(left, fg_color=BORDER, width=1, height=20).pack(side="left", padx=10)

        # ─── Right: Window controls ──────────────────
        right = ctk.CTkFrame(toolbar, fg_color="transparent")
        right.pack(side="right", padx=(0, 16))

        # Screen capture switch
        self._hide_var = ctk.BooleanVar(value=True)
        self._hide_switch = ctk.CTkSwitch(
            right, text=t("hidden"), variable=self._hide_var,
            command=self._toggle_capture,
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
            text_color=TEXT_MUTED,
            progress_color=ACCENT, button_color=TEXT_PRIMARY,
            button_hover_color=TEXT_WHITE, fg_color=SURFACE,
            width=36, height=16,
        )
        self._hide_switch.pack(side="right", padx=(10, 0))

        self._screen_label = ctk.CTkLabel(
            right, text=t("screen"),
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
            text_color=TEXT_MUTED,
        )
        self._screen_label.pack(side="right")

        ctk.CTkFrame(right, fg_color=BORDER, width=1, height=20).pack(side="right", padx=10)

        # Pin button (filled when active, ghost when inactive)
        self._pinned = True
        self._pin_btn = ctk.CTkButton(
            right, text=t("pin"), width=46, height=28,
            font=ctk.CTkFont(family=FONT_FAMILY, size=11),
            fg_color=ACCENT, hover_color=ACCENT_HOVER,
            text_color=TEXT_WHITE, corner_radius=6,
            command=self._toggle_pin,
        )
        self._pin_btn.pack(side="right")

        ctk.CTkFrame(right, fg_color=BORDER, width=1, height=20).pack(side="right", padx=10)

        # ─── Center-right: Navigation ────────────────
        nav = ctk.CTkFrame(toolbar, fg_color="transparent")
        nav.pack(side="right", padx=(0, 6))

        # History button (ghost)
        self._history_btn = ctk.CTkButton(
            nav, text=t("history"), width=68, height=28,
            font=ctk.CTkFont(family=FONT_FAMILY, size=11),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            command=self._open_history_modal,
        )
        self._history_btn.pack(side="right", padx=(4, 0))

    def _build_project_bar(self):
        self._project_bar = ctk.CTkFrame(self, fg_color=BG_HEADER, height=36, corner_radius=0)
        # Only show in daily mode
        if self._current_mode == MODE_DAILY:
            self._project_bar.pack(fill="x", before=self._content_frame)
        self._project_bar.pack_propagate(False)

        ctk.CTkFrame(self._project_bar, fg_color=BORDER, height=1).place(relx=0, rely=0, relwidth=1)

        self._project_path_display = ctk.CTkLabel(
            self._project_bar,
            text=self._project_path if self._project_path else t("select_project"),
            font=ctk.CTkFont(family=FONT_MONO, size=10),
            text_color=TEXT_PRIMARY if self._project_path else TEXT_MUTED,
            anchor="w",
        )
        self._project_path_display.pack(side="left", fill="x", expand=True, padx=(16, 6))

        if self._project_path:
            self._project_clear_btn = ctk.CTkButton(
                self._project_bar, text="X", width=28, height=24,
                font=ctk.CTkFont(family=FONT_FAMILY, size=10),
                fg_color="transparent", hover_color=BTN_HOVER,
                text_color=TEXT_MUTED, corner_radius=4,
                command=self._clear_project,
            )
            self._project_clear_btn.pack(side="right", padx=(0, 4))

        self._project_select_btn = ctk.CTkButton(
            self._project_bar, text=t("select_folder"), width=78, height=24,
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            border_width=1, border_color=BORDER,
            command=self._select_project_folder,
        )
        self._project_select_btn.pack(side="right", padx=(0, 12))

    def _build_panels(self):
        content = ctk.CTkFrame(self, fg_color=BG_PRIMARY)
        self._content_frame = content
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

        # Panel header with accent bar
        panel_header = ctk.CTkFrame(frame, fg_color="transparent", height=36)
        panel_header.pack(fill="x", padx=8, pady=(8, 0))
        panel_header.pack_propagate(False)

        # Left accent bar
        ctk.CTkFrame(
            panel_header, fg_color=icon_color, width=3, corner_radius=2,
        ).pack(side="left", fill="y", padx=(0, 10))

        ctk.CTkLabel(
            panel_header, text=title,
            font=ctk.CTkFont(family=FONT_FAMILY, size=12, weight="bold"),
            text_color=TEXT_PRIMARY,
        ).pack(side="left", anchor="w")

        # Text area
        textbox = ctk.CTkTextbox(
            frame, fg_color=BG_TEXT, text_color=TEXT_PRIMARY,
            font=ctk.CTkFont(family=FONT_MONO, size=12),
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
            statusbar, text=t("starting"),
            font=ctk.CTkFont(family=FONT_FAMILY, size=10),
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
            label = t("hidden") if self._capture_guard.is_hidden else t("visible")
            self._hide_switch.configure(text=label)

    def _toggle_pin(self):
        self._pinned = not self._pinned
        self.attributes("-topmost", self._pinned)
        if self._pinned:
            self._pin_btn.configure(fg_color=ACCENT, hover_color=ACCENT_HOVER, text_color=TEXT_WHITE)
        else:
            self._pin_btn.configure(fg_color="transparent", hover_color=BTN_HOVER, text_color=TEXT_SECONDARY)

    def _change_opacity(self, value):
        self.attributes("-alpha", value)

    # ─── Language ─────────────────────────────────────────────

    def _set_ui_lang(self, lang: str):
        set_lang(lang)
        # Update toggle visuals
        is_es = lang == "es"
        self._lang_es_btn.configure(
            fg_color=ACCENT if is_es else "transparent",
            text_color=TEXT_WHITE if is_es else TEXT_MUTED,
        )
        self._lang_en_btn.configure(
            fg_color=ACCENT if not is_es else "transparent",
            text_color=TEXT_WHITE if not is_es else TEXT_MUTED,
        )

    def _on_language_changed(self, lang: str):
        self._opacity_label.configure(text=t("opacity"))
        self._screenshot_btn.configure(text=t("screenshot"))
        self._rec_btn.configure(text=t("record_5s"))
        self._screen_label.configure(text=t("screen"))
        self._update_capture_label()
        self._pin_btn.configure(text=t("pin"))
        self._history_btn.configure(text=t("history"))
        self._docs_btn.configure(text=t("docs"))
        self._docs_daily_btn.configure(text=t("docs_daily"))
        self._jd_btn.configure(text=t("job_desc"))
        self._mode_interview_btn.configure(text=t("mode_interview"))
        self._mode_daily_btn.configure(text=t("mode_daily"))
        self._project_select_btn.configure(text=t("select_folder"))
        if not self._project_path:
            self._project_path_display.configure(text=t("select_project"))
        if self._device_name:
            self._listening_status = t("listening", device=self._device_name)
            self._set_status(self._listening_status)

    # ─── Mode & Project ─────────────────────────────────────────

    def _switch_mode(self, mode: str):
        if mode == self._current_mode:
            return
        self._current_mode = mode

        if mode == MODE_INTERVIEW:
            self._mode_interview_btn.configure(
                fg_color=ACCENT, hover_color=ACCENT_HOVER,
                text_color=TEXT_WHITE, border_width=0,
            )
            self._mode_daily_btn.configure(
                fg_color="transparent", hover_color=BTN_HOVER,
                text_color=TEXT_SECONDARY, border_width=1, border_color=BORDER,
            )
            self._project_bar.pack_forget()
            # Show interview nav, hide daily nav
            self._jd_btn.pack(side="right", padx=(4, 0))
            self._docs_btn.pack(side="right", padx=(4, 0))
            self._docs_daily_btn.pack_forget()
        else:
            self._mode_daily_btn.configure(
                fg_color=ACCENT, hover_color=ACCENT_HOVER,
                text_color=TEXT_WHITE, border_width=0,
            )
            self._mode_interview_btn.configure(
                fg_color="transparent", hover_color=BTN_HOVER,
                text_color=TEXT_SECONDARY, border_width=1, border_color=BORDER,
            )
            self._project_bar.pack(fill="x", before=self._content_frame)
            # Show daily nav, hide interview nav
            self._jd_btn.pack_forget()
            self._docs_btn.pack_forget()
            self._docs_daily_btn.pack(side="right", padx=(4, 0))

        if self._ai_provider:
            self._ai_provider.set_mode(mode)

    def _select_project_folder(self):
        from tkinter import filedialog
        folder = filedialog.askdirectory(title=t("select_folder"))
        if folder:
            self._project_path = folder
            self._save_project_path(folder)
            self._project_path_display.configure(
                text=folder, text_color=TEXT_PRIMARY,
            )
            self._load_project_context()
            name = os.path.basename(folder)
            self._set_status(t("project_loaded", name=name))
            # Add clear button if not present
            if not hasattr(self, "_project_clear_btn"):
                self._project_clear_btn = ctk.CTkButton(
                    self._project_bar, text="X", width=28, height=24,
                    font=ctk.CTkFont(family=FONT_FAMILY, size=10),
                    fg_color="transparent", hover_color=BTN_HOVER,
                    text_color=TEXT_MUTED, corner_radius=4,
                    command=self._clear_project,
                )
                self._project_clear_btn.pack(side="right", padx=(0, 4), before=self._project_select_btn)

    def _clear_project(self):
        self._project_path = ""
        self._save_project_path("")
        self._project_path_display.configure(
            text=t("select_project"), text_color=TEXT_MUTED,
        )
        if self._ai_provider:
            self._ai_provider.set_project_context("")
        if hasattr(self, "_project_clear_btn"):
            self._project_clear_btn.destroy()
            del self._project_clear_btn
        self._set_status(t("project_cleared"))

    def _load_project_context(self):
        if self._project_path and self._ai_provider:
            context = scan_project(self._project_path)
            self._ai_provider.set_project_context(context)

    @staticmethod
    def _load_project_path() -> str:
        try:
            with open(PROJECT_PATH_FILE, "r", encoding="utf-8") as f:
                path = f.read().strip()
                if path and os.path.isdir(path):
                    return path
        except (OSError, ValueError):
            pass
        return ""

    @staticmethod
    def _save_project_path(path: str):
        try:
            os.makedirs(os.path.dirname(PROJECT_PATH_FILE), exist_ok=True)
            with open(PROJECT_PATH_FILE, "w", encoding="utf-8") as f:
                f.write(path)
        except OSError:
            pass

    # ─── Screen Capture ───────────────────────────────────────

    def _take_screenshot(self):
        if not self._ai_provider:
            self._set_status(t("ai_not_ready"))
            return
        self._set_status(t("capturing_screenshot"))
        self.iconify()
        self.after(CAPTURE_DELAY_MS, self._do_screenshot)

    def _do_screenshot(self):
        def capture():
            img_b64 = self._screen_capture.screenshot()
            self.after(0, self.deiconify)
            self._emit("ai_thinking", "", "")
            answer = self._ai_provider.ask_with_images(
                [img_b64], TARGET_LANG, self._get_chat_history(),
            )
            timestamp = datetime.now().strftime("%H:%M:%S")
            self._emit("ai", answer, timestamp)
            if self._listening_status:
                self._emit("status", self._listening_status)
        threading.Thread(target=capture, daemon=True).start()

    def _record_screen(self):
        if not self._ai_provider:
            self._set_status(t("ai_not_ready"))
            return
        self._rec_btn.configure(fg_color=DANGER, hover_color=DANGER_HOVER, text_color=TEXT_WHITE, border_color=DANGER, text=t("recording"))
        self._set_status(t("recording_screen"))
        self.iconify()
        self.after(CAPTURE_DELAY_MS, self._do_record)

    def _do_record(self):
        def capture():
            frames = self._screen_capture.record_frames(duration=5, fps=2)
            self.after(0, self.deiconify)
            self.after(0, lambda: self._rec_btn.configure(fg_color="transparent", hover_color=BTN_HOVER, text_color=TEXT_SECONDARY, border_color=BORDER, text=t("record_5s")))
            self._emit("ai_thinking", "", "")
            answer = self._ai_provider.ask_with_images(
                frames, TARGET_LANG, self._get_chat_history(),
            )
            timestamp = datetime.now().strftime("%H:%M:%S")
            self._emit("ai", answer, timestamp)
            if self._listening_status:
                self._emit("status", self._listening_status)
        threading.Thread(target=capture, daemon=True).start()

    def _get_chat_history(self) -> list[dict]:
        if hasattr(self, "_silence_monitor"):
            return self._silence_monitor.chat_history
        return []

    def _open_job_modal(self):
        current = JobDescription.load()
        JobDescriptionModal(self, current, self._save_job_description)

    def _open_docs_modal(self):
        DocsModal(self, self._reload_docs, DOCS_FOLDER)

    def _open_docs_daily_modal(self):
        DocsModal(self, self._reload_daily_docs, DOCS_DAILY_FOLDER)

    def _reload_docs(self):
        """Recarga documentos y actualiza el contexto del AI provider."""
        loader = DocumentLoader(DOCS_FOLDER)
        docs_context, files = loader.load_all()
        if self._ai_provider:
            self._ai_provider.set_context(docs_context)
        has_docs = len(files) > 0
        self._docs_btn.configure(
            border_color=STATE_ACTIVE if has_docs else BORDER,
            text_color=TEXT_PRIMARY if has_docs else TEXT_SECONDARY,
        )
        self._set_status(f"Docs: {', '.join(files)}" if files else t("no_docs"))

    def _reload_daily_docs(self):
        """Recarga documentos daily y actualiza el contexto del AI provider."""
        loader = DocumentLoader(DOCS_DAILY_FOLDER)
        docs_context, files = loader.load_all()
        if self._ai_provider:
            self._ai_provider.set_daily_context(docs_context)
        has_docs = len(files) > 0
        self._docs_daily_btn.configure(
            border_color=STATE_ACTIVE if has_docs else BORDER,
            text_color=TEXT_PRIMARY if has_docs else TEXT_SECONDARY,
        )
        self._set_status(f"Docs Daily: {', '.join(files)}" if files else t("no_docs"))

    def _open_history_modal(self):
        HistoryModal(self)

    def _save_job_description(self, text: str):
        JobDescription.save(text)
        if self._ai_provider:
            self._ai_provider.set_job_description(text)
        has_jd = bool(text.strip())
        self._jd_btn.configure(
            border_color=STATE_ACTIVE if has_jd else BORDER,
            text_color=TEXT_PRIMARY if has_jd else TEXT_SECONDARY,
        )
        self._set_status(t("jd_saved") if has_jd else t("jd_deleted"))

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
            self.after(UI_POLL_INTERVAL_MS, self._poll_ui_queue)

    def _handle_event(self, event_type: str, text: str, timestamp: str):
        handlers = {
            "interviewer": lambda: self._append_to(
                self._interviewer_text, text, timestamp, "original"),
            "interviewer_translation": lambda: self._append_to(
                self._interviewer_text, f"  ↳ {text}", timestamp, "translation"),
            "ai_thinking": lambda: self._append_to(
                self._ai_text, "⏳", "", "thinking"),
            "ai": lambda: self._append_to(
                self._ai_text, text + "\n", timestamp, "answer"),
            "ai_translation": lambda: self._append_to(
                self._ai_text, f"↳ {text}\n", timestamp, "translation"),
            "status": lambda: self._set_status(text),
            "status_restore": lambda: self._set_status(self._listening_status) if self._listening_status else None,
            "docs_loaded": lambda: self._docs_btn.configure(
                border_color=STATE_ACTIVE if int(text) > 0 else BORDER,
                text_color=TEXT_PRIMARY if int(text) > 0 else TEXT_SECONDARY),
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
        self._emit("status", t("loading_docs"))
        loader = DocumentLoader(DOCS_FOLDER)
        docs_context, files = loader.load_all()
        self._emit("docs_loaded", str(len(files)), "")
        if files:
            self._emit("status", f"Docs: {', '.join(files)}")

        # AI Provider
        ai_provider = create_ai_provider(AI_PROVIDER)
        ai_provider.set_context(docs_context)
        ai_provider.set_job_description(JobDescription.load())
        ai_provider.set_mode(self._current_mode)

        # Load daily docs context
        daily_loader = DocumentLoader(DOCS_DAILY_FOLDER)
        daily_context, _ = daily_loader.load_all()
        ai_provider.set_daily_context(daily_context)

        self._ai_provider = ai_provider

        # Load project context if path is saved
        if self._project_path:
            context = scan_project(self._project_path)
            ai_provider.set_project_context(context)

        # Whisper
        self._emit("status", t("loading_whisper"))
        transcriber = Transcriber()
        transcriber.load_model()

        # Translator
        translator = TextTranslator(TARGET_LANG)

        # Silence Monitor
        self._silence_monitor = SilenceMonitor(ai_provider, translator, self._emit)

        # Audio Capture
        self._emit("status", t("searching_audio"))
        audio_capture = AudioCapture(self._audio_queue)
        try:
            device_name = audio_capture.initialize()
            self._device_name = device_name[:30]
            self._listening_status = t("listening", device=self._device_name)
            self._emit("status", self._listening_status)
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

                self._silence_monitor.record_speech(result.text, result.language)
                self._emit("interviewer", result.text, result.timestamp)

                if translator.needs_translation(result.language):
                    translated = translator.translate(result.text)
                    if translated:
                        self._emit("interviewer_translation", translated, result.timestamp)

        # Launch threads
        threads = [
            threading.Thread(target=audio_capture.run, daemon=True),
            threading.Thread(target=process_loop, daemon=True),
            threading.Thread(target=self._silence_monitor.run, daemon=True),
        ]
        for th in threads:
            th.start()

    def _on_closing(self):
        self._running = False
        remove_lang_callback(self._on_language_changed)
        self.destroy()
