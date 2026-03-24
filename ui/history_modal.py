import os
import re

import customtkinter as ctk

from config import HISTORY_DIR
from ui.i18n import t
from ui.theme import (
    BG_PRIMARY, BG_HEADER, BG_TEXT, ACCENT, ACCENT_WARM,
    TEXT_PRIMARY, TEXT_SECONDARY, TEXT_MUTED, TEXT_TRANSLATION,
    SURFACE, BORDER, BTN_HOVER,
)


class HistoryModal(ctk.CTkToplevel):
    """Modal para visualizar el historial de conversaciones."""

    def __init__(self, parent):
        super().__init__(parent)

        self._history_dir = HISTORY_DIR
        self._sessions: list[dict] = []

        self.withdraw()

        self.title(t("history_title"))
        self.geometry("900x600")
        self.configure(fg_color=BG_PRIMARY)
        self.resizable(True, True)
        self.minsize(700, 400)
        self.transient(parent)
        self.grab_set()

        self._build_ui()
        self._load_sessions()

        self._center_on(parent)
        self.deiconify()

    def _center_on(self, parent):
        parent.update_idletasks()
        px = parent.winfo_x() + (parent.winfo_width() // 2) - 450
        py = parent.winfo_y() + (parent.winfo_height() // 2) - 300
        self.geometry(f"900x600+{px}+{py}")

    # ─── UI Build ─────────────────────────────────────────────

    def _build_ui(self):
        self._build_header()
        self._build_body()

    def _build_header(self):
        header = ctk.CTkFrame(self, fg_color=BG_HEADER, height=50, corner_radius=0)
        header.pack(fill="x")
        header.pack_propagate(False)

        # Accent bar + title
        ctk.CTkFrame(
            header, fg_color=ACCENT, width=3, corner_radius=2,
        ).pack(side="left", fill="y", padx=(16, 10), pady=10)

        ctk.CTkLabel(
            header, text=t("history_header"),
            font=ctk.CTkFont(family="Segoe UI", size=14, weight="bold"),
            text_color=TEXT_PRIMARY,
        ).pack(side="left")

        self._session_count_label = ctk.CTkLabel(
            header, text="",
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        )
        self._session_count_label.pack(side="left", padx=(10, 0))

        # Close button (ghost)
        ctk.CTkButton(
            header, text=t("close"), width=70, height=28,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            border_width=1, border_color=BORDER,
            command=self.destroy,
        ).pack(side="right", padx=16)

        ctk.CTkFrame(self, fg_color=BORDER, height=1).pack(fill="x")

    def _build_body(self):
        body = ctk.CTkFrame(self, fg_color=BG_PRIMARY)
        body.pack(fill="both", expand=True)
        body.grid_columnconfigure(0, weight=0, minsize=220)
        body.grid_columnconfigure(1, weight=1)
        body.grid_rowconfigure(0, weight=1)

        self._build_sidebar(body)
        self._build_viewer(body)

    def _build_sidebar(self, parent):
        sidebar = ctk.CTkFrame(parent, fg_color=BG_HEADER, corner_radius=0, width=220)
        sidebar.grid(row=0, column=0, sticky="nsew")
        sidebar.grid_propagate(False)

        # Sidebar header
        sidebar_header = ctk.CTkFrame(sidebar, fg_color="transparent", height=36)
        sidebar_header.pack(fill="x", padx=12, pady=(12, 4))
        sidebar_header.pack_propagate(False)

        ctk.CTkLabel(
            sidebar_header, text=t("sessions"),
            font=ctk.CTkFont(family="Segoe UI", size=12, weight="bold"),
            text_color=TEXT_SECONDARY,
        ).pack(side="left")

        ctk.CTkFrame(sidebar, fg_color=BORDER, height=1).pack(fill="x", padx=12)

        # Scrollable list
        self._session_list = ctk.CTkScrollableFrame(
            sidebar, fg_color="transparent",
            scrollbar_button_color=SURFACE,
            scrollbar_button_hover_color=BORDER,
        )
        self._session_list.pack(fill="both", expand=True, padx=4, pady=4)

        # Vertical separator
        ctk.CTkFrame(parent, fg_color=BORDER, width=1).grid(row=0, column=0, sticky="nse")

    def _build_viewer(self, parent):
        viewer = ctk.CTkFrame(parent, fg_color=BG_PRIMARY, corner_radius=0)
        viewer.grid(row=0, column=1, sticky="nsew")

        self._empty_label = ctk.CTkLabel(
            viewer, text=t("select_session"),
            font=ctk.CTkFont(family="Segoe UI", size=13),
            text_color=TEXT_MUTED,
        )
        self._empty_label.place(relx=0.5, rely=0.5, anchor="center")

        self._viewer_text = ctk.CTkTextbox(
            viewer, fg_color=BG_TEXT, text_color=TEXT_PRIMARY,
            font=ctk.CTkFont(family="Consolas", size=12),
            corner_radius=0, wrap="word", state="disabled",
            border_width=0,
        )

        # Configure tags
        tb = self._viewer_text._textbox
        tb.tag_configure("session_header", foreground=ACCENT, font=("Segoe UI", 13, "bold"))
        tb.tag_configure("separator", foreground=TEXT_MUTED)
        tb.tag_configure("timestamp", foreground=TEXT_MUTED)
        tb.tag_configure("interviewer_label", foreground=ACCENT)
        tb.tag_configure("interviewer_text", foreground=TEXT_PRIMARY)
        tb.tag_configure("ai_label", foreground=ACCENT_WARM)
        tb.tag_configure("ai_text", foreground=ACCENT_WARM)
        tb.tag_configure("translation_label", foreground=TEXT_TRANSLATION)
        tb.tag_configure("translation_text", foreground=TEXT_TRANSLATION)

    # ─── Data Loading ─────────────────────────────────────────

    def _load_sessions(self):
        if not os.path.isdir(self._history_dir):
            self._session_count_label.configure(text=t("no_history"))
            return

        files = sorted(
            [f for f in os.listdir(self._history_dir) if f.endswith(".txt")],
            reverse=True,
        )

        if not files:
            self._session_count_label.configure(text=t("no_history"))
            return

        grouped: dict[str, list[str]] = {}
        for f in files:
            match = re.match(r"session_(\d{4}-\d{2}-\d{2})_", f)
            if match:
                date = match.group(1)
                grouped.setdefault(date, []).append(f)

        self._session_count_label.configure(text=t("n_sessions", n=len(files)))

        for date, session_files in grouped.items():
            self._add_date_group(date, session_files)

    def _add_date_group(self, date: str, files: list[str]):
        ctk.CTkLabel(
            self._session_list, text=f"  {date}",
            font=ctk.CTkFont(family="Segoe UI", size=11, weight="bold"),
            text_color=TEXT_MUTED, anchor="w",
        ).pack(fill="x", pady=(10, 2))

        for filename in files:
            match = re.match(r"session_\d{4}-\d{2}-\d{2}_(\d{2})-(\d{2})-(\d{2})\.txt", filename)
            time_str = f"{match.group(1)}:{match.group(2)}:{match.group(3)}" if match else filename

            ctk.CTkButton(
                self._session_list,
                text=f"    {time_str}",
                width=190, height=30, anchor="w",
                font=ctk.CTkFont(family="Segoe UI", size=11),
                fg_color="transparent", hover_color=BTN_HOVER,
                text_color=TEXT_SECONDARY, corner_radius=6,
                command=lambda f=filename: self._show_session(f),
            ).pack(fill="x", pady=1)

    # ─── Session Viewer ───────────────────────────────────────

    def _show_session(self, filename: str):
        filepath = os.path.join(self._history_dir, filename)

        try:
            with open(filepath, "r", encoding="utf-8") as f:
                content = f.read()
        except (OSError, UnicodeDecodeError):
            return

        self._empty_label.place_forget()
        self._viewer_text.place(relx=0, rely=0, relwidth=1, relheight=1)

        self._viewer_text.configure(state="normal")
        self._viewer_text._textbox.delete("1.0", "end")

        self._render_conversation(content)

        self._viewer_text._textbox.see("1.0")
        self._viewer_text.configure(state="disabled")

    def _render_conversation(self, content: str):
        tb = self._viewer_text._textbox

        for line in content.splitlines():
            if line.startswith("Session:"):
                tb.insert("end", f"{line}\n", "session_header")
            elif line.startswith("=" * 10):
                tb.insert("end", f"{line}\n\n", "separator")
            elif "] INTERVIEWER:" in line:
                ts, text = self._parse_log_line(line, "INTERVIEWER:")
                if ts:
                    tb.insert("end", f"{ts}  ", "timestamp")
                tb.insert("end", "INTERVIEWER  ", "interviewer_label")
                tb.insert("end", f"{text}\n", "interviewer_text")
            elif "] AI RESPONSE:" in line:
                ts, text = self._parse_log_line(line, "AI RESPONSE:")
                if ts:
                    tb.insert("end", f"{ts}  ", "timestamp")
                tb.insert("end", "AI  ", "ai_label")
                tb.insert("end", f"{text}\n", "ai_text")
            elif "TRADUCCION:" in line:
                text = line.split("TRADUCCION:", 1)[1].strip()
                tb.insert("end", "           ", "timestamp")
                tb.insert("end", f"↳ {text}\n", "translation_text")
            elif line.strip():
                tb.insert("end", f"{line}\n", "interviewer_text")
            else:
                tb.insert("end", "\n")

    @staticmethod
    def _parse_log_line(line: str, label: str) -> tuple[str, str]:
        match = re.match(r"\[([^\]]+)\]\s+" + re.escape(label) + r"\s*(.*)", line)
        if match:
            return match.group(1), match.group(2)
        return "", line
