import customtkinter as ctk

from ui.i18n import t
from ui.theme import (
    BG_PRIMARY, BG_HEADER, BG_TEXT, ACCENT,
    TEXT_PRIMARY, TEXT_MUTED, TEXT_SECONDARY,
    SURFACE, BORDER, BTN_HOVER, SUCCESS, SUCCESS_HOVER,
)


class JobDescriptionModal(ctk.CTkToplevel):
    """Modal para editar la Job Description."""

    def __init__(self, parent, current_text: str, on_save):
        super().__init__(parent)

        self._on_save = on_save

        self.withdraw()

        self.title("Job Description")
        self.geometry("600x500")
        self.configure(fg_color=BG_PRIMARY)
        self.resizable(True, True)
        self.minsize(400, 300)
        self.transient(parent)
        self.grab_set()

        self._build_ui(current_text)

        self._center_on(parent)
        self.deiconify()

    def _center_on(self, parent):
        parent.update_idletasks()
        px = parent.winfo_x() + (parent.winfo_width() // 2) - 300
        py = parent.winfo_y() + (parent.winfo_height() // 2) - 250
        self.geometry(f"600x500+{px}+{py}")

    def _build_ui(self, current_text: str):
        self._build_header()
        self._build_editor(current_text)
        self._build_footer()

    def _build_header(self):
        header = ctk.CTkFrame(self, fg_color=BG_HEADER, height=50, corner_radius=0)
        header.pack(fill="x")
        header.pack_propagate(False)

        # Accent bar + title
        ctk.CTkFrame(
            header, fg_color=ACCENT, width=3, corner_radius=2,
        ).pack(side="left", fill="y", padx=(16, 10), pady=10)

        ctk.CTkLabel(
            header, text=t("jd_title"),
            font=ctk.CTkFont(family="Segoe UI", size=14, weight="bold"),
            text_color=TEXT_PRIMARY,
        ).pack(side="left")

        ctk.CTkLabel(
            header, text=t("jd_subtitle"),
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        ).pack(side="left", padx=(12, 0))

        ctk.CTkFrame(self, fg_color=BORDER, height=1).pack(fill="x")

    def _build_editor(self, current_text: str):
        self._textbox = ctk.CTkTextbox(
            self, fg_color=BG_TEXT, text_color=TEXT_PRIMARY,
            font=ctk.CTkFont(family="Consolas", size=12),
            corner_radius=0, wrap="word", border_width=0,
        )
        self._textbox.pack(fill="both", expand=True, padx=0, pady=0)

        if current_text:
            self._textbox.insert("1.0", current_text)

    def _build_footer(self):
        ctk.CTkFrame(self, fg_color=BORDER, height=1).pack(fill="x")

        footer = ctk.CTkFrame(self, fg_color=BG_HEADER, height=50, corner_radius=0)
        footer.pack(fill="x", side="bottom")
        footer.pack_propagate(False)

        # Character count (left)
        self._char_label = ctk.CTkLabel(
            footer, text=t("characters", n=0),
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        )
        self._char_label.pack(side="left", padx=16)

        # Buttons (right)
        btn_frame = ctk.CTkFrame(footer, fg_color="transparent")
        btn_frame.pack(side="right", padx=16)

        # Cancel (ghost)
        ctk.CTkButton(
            btn_frame, text=t("cancel"), width=80, height=30,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color="transparent", hover_color=BTN_HOVER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            border_width=1, border_color=BORDER,
            command=self.destroy,
        ).pack(side="left", padx=(0, 8))

        # Save (primary)
        ctk.CTkButton(
            btn_frame, text=t("save"), width=80, height=30,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=SUCCESS, hover_color=SUCCESS_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=self._save,
        ).pack(side="left")

        self._textbox.bind("<KeyRelease>", self._update_count)
        self._update_count()

    def _update_count(self, event=None):
        text = self._textbox.get("1.0", "end-1c")
        self._char_label.configure(text=t("characters", n=len(text)))

    def _save(self):
        text = self._textbox.get("1.0", "end-1c")
        self._on_save(text)
        self.destroy()
