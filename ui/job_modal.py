import customtkinter as ctk

# ─── Theme (same as app.py) ──────────────────────────────────
BG_PRIMARY = "#0c0c14"
BG_HEADER = "#12121e"
BG_TEXT = "#0a0a12"
ACCENT = "#3b82f6"
ACCENT_HOVER = "#2563eb"
TEXT_PRIMARY = "#e2e8f0"
TEXT_SECONDARY = "#94a3b8"
TEXT_MUTED = "#475569"
SURFACE = "#1e1e2e"
BORDER = "#1e293b"
SUCCESS = "#22c55e"
SUCCESS_HOVER = "#16a34a"


class JobDescriptionModal(ctk.CTkToplevel):
    """Modal para editar la Job Description."""

    def __init__(self, parent, current_text: str, on_save):
        super().__init__(parent)

        self._on_save = on_save

        # Window config
        self.title("Job Description")
        self.geometry("600x500")
        self.configure(fg_color=BG_PRIMARY)
        self.resizable(True, True)
        self.minsize(400, 300)
        self.transient(parent)
        self.grab_set()

        # Center on parent
        self.after(10, lambda: self._center_on(parent))

        self._build_ui(current_text)

    def _center_on(self, parent):
        parent.update_idletasks()
        px = parent.winfo_x() + (parent.winfo_width() // 2) - 300
        py = parent.winfo_y() + (parent.winfo_height() // 2) - 250
        self.geometry(f"600x500+{px}+{py}")

    def _build_ui(self, current_text: str):
        # Header
        header = ctk.CTkFrame(self, fg_color=BG_HEADER, height=50, corner_radius=0)
        header.pack(fill="x")
        header.pack_propagate(False)

        ctk.CTkLabel(
            header, text="Job Description",
            font=ctk.CTkFont(family="Segoe UI", size=15, weight="bold"),
            text_color=TEXT_PRIMARY,
        ).pack(side="left", padx=20)

        ctk.CTkLabel(
            header, text="Pega la descripcion del puesto al que aplicas",
            font=ctk.CTkFont(family="Segoe UI", size=11),
            text_color=TEXT_MUTED,
        ).pack(side="left", padx=(8, 0))

        # Separator
        ctk.CTkFrame(self, fg_color=BORDER, height=1).pack(fill="x")

        # Text area
        self._textbox = ctk.CTkTextbox(
            self, fg_color=BG_TEXT, text_color=TEXT_PRIMARY,
            font=ctk.CTkFont(family="Consolas", size=12),
            corner_radius=0, wrap="word", border_width=0,
        )
        self._textbox.pack(fill="both", expand=True, padx=0, pady=0)

        if current_text:
            self._textbox.insert("1.0", current_text)

        # Footer
        footer = ctk.CTkFrame(self, fg_color=BG_HEADER, height=50, corner_radius=0)
        footer.pack(fill="x", side="bottom")
        footer.pack_propagate(False)

        # Separator
        ctk.CTkFrame(footer, fg_color=BORDER, height=1).place(relx=0, rely=0, relwidth=1)

        # Buttons
        btn_frame = ctk.CTkFrame(footer, fg_color="transparent")
        btn_frame.pack(side="right", padx=16)

        ctk.CTkButton(
            btn_frame, text="Cancelar", width=80, height=30,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=SURFACE, hover_color=BORDER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            command=self.destroy,
        ).pack(side="left", padx=(0, 8))

        ctk.CTkButton(
            btn_frame, text="Guardar", width=80, height=30,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=SUCCESS, hover_color=SUCCESS_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=self._save,
        ).pack(side="left")

        # Character count
        self._char_label = ctk.CTkLabel(
            footer, text="0 caracteres",
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        )
        self._char_label.pack(side="left", padx=16)

        self._textbox.bind("<KeyRelease>", self._update_count)
        self._update_count()

    def _update_count(self, event=None):
        text = self._textbox.get("1.0", "end-1c")
        self._char_label.configure(text=f"{len(text)} caracteres")

    def _save(self):
        text = self._textbox.get("1.0", "end-1c")
        self._on_save(text)
        self.destroy()
