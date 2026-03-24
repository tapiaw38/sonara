import os
import shutil
from tkinter import filedialog

import customtkinter as ctk

from config import DOCS_FOLDER
from ui.i18n import t

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
DANGER = "#ef4444"
DANGER_HOVER = "#dc2626"

SUPPORTED_EXTENSIONS = (".pdf", ".txt", ".md", ".csv")


class DocsModal(ctk.CTkToplevel):
    """Modal para gestionar documentos de contexto."""

    def __init__(self, parent, on_docs_changed):
        super().__init__(parent)

        self._on_docs_changed = on_docs_changed

        self.withdraw()

        self.title(t("documents"))
        self.geometry("550x450")
        self.configure(fg_color=BG_PRIMARY)
        self.resizable(True, True)
        self.minsize(400, 300)
        self.transient(parent)
        self.grab_set()

        self._build_ui()
        self._refresh_list()

        self._center_on(parent)
        self.deiconify()

    def _center_on(self, parent):
        parent.update_idletasks()
        px = parent.winfo_x() + (parent.winfo_width() // 2) - 275
        py = parent.winfo_y() + (parent.winfo_height() // 2) - 225
        self.geometry(f"550x450+{px}+{py}")

    # ─── UI Build ─────────────────────────────────────────────

    def _build_ui(self):
        self._build_header()
        self._build_doc_list()
        self._build_footer()

    def _build_header(self):
        header = ctk.CTkFrame(self, fg_color=BG_HEADER, height=50, corner_radius=0)
        header.pack(fill="x")
        header.pack_propagate(False)

        ctk.CTkLabel(
            header, text=t("documents"),
            font=ctk.CTkFont(family="Segoe UI", size=15, weight="bold"),
            text_color=TEXT_PRIMARY,
        ).pack(side="left", padx=20)

        self._count_label = ctk.CTkLabel(
            header, text="",
            font=ctk.CTkFont(family="Segoe UI", size=11),
            text_color=TEXT_MUTED,
        )
        self._count_label.pack(side="left", padx=(8, 0))

        # Add button in header
        ctk.CTkButton(
            header, text=t("add"), width=90, height=28,
            font=ctk.CTkFont(family="Segoe UI", size=11, weight="bold"),
            fg_color=SUCCESS, hover_color=SUCCESS_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=self._add_documents,
        ).pack(side="right", padx=16)

        # Separator
        ctk.CTkFrame(self, fg_color=BORDER, height=1).pack(fill="x")

    def _build_doc_list(self):
        # Scrollable container for document items
        self._list_frame = ctk.CTkScrollableFrame(
            self, fg_color=BG_PRIMARY,
            scrollbar_button_color=SURFACE,
            scrollbar_button_hover_color=BORDER,
        )
        self._list_frame.pack(fill="both", expand=True, padx=0, pady=0)

        # Empty state
        self._empty_label = ctk.CTkLabel(
            self._list_frame,
            text=t("no_docs_loaded"),
            font=ctk.CTkFont(family="Segoe UI", size=12),
            text_color=TEXT_MUTED, justify="center",
        )

    def _build_footer(self):
        # Separator
        ctk.CTkFrame(self, fg_color=BORDER, height=1).pack(fill="x")

        footer = ctk.CTkFrame(self, fg_color=BG_HEADER, height=44, corner_radius=0)
        footer.pack(fill="x", side="bottom")
        footer.pack_propagate(False)

        ctk.CTkLabel(
            footer, text=t("supported_formats"),
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        ).pack(side="left", padx=16)

        ctk.CTkButton(
            footer, text=t("close"), width=70, height=28,
            font=ctk.CTkFont(family="Segoe UI", size=11),
            fg_color=SURFACE, hover_color=BORDER,
            text_color=TEXT_SECONDARY, corner_radius=6,
            command=self.destroy,
        ).pack(side="right", padx=16)

    # ─── Document List ────────────────────────────────────────

    def _refresh_list(self):
        # Clear existing items
        for widget in self._list_frame.winfo_children():
            if widget != self._empty_label:
                widget.destroy()

        files = self._get_doc_files()

        if not files:
            self._empty_label.pack(pady=40)
            self._count_label.configure(text=t("n_documents", n=0))
            return

        self._empty_label.pack_forget()
        key = "n_documents" if len(files) != 1 else "n_document"
        self._count_label.configure(text=t(key, n=len(files)))

        for filename in files:
            self._create_doc_row(filename)

    def _create_doc_row(self, filename: str):
        filepath = os.path.join(DOCS_FOLDER, filename)
        size = os.path.getsize(filepath)
        size_str = self._format_size(size)
        ext = os.path.splitext(filename)[1].upper().replace(".", "")

        row = ctk.CTkFrame(self._list_frame, fg_color=SURFACE, height=44, corner_radius=8)
        row.pack(fill="x", padx=12, pady=3)
        row.pack_propagate(False)

        # File type badge
        ctk.CTkLabel(
            row, text=ext, width=40,
            font=ctk.CTkFont(family="Segoe UI", size=9, weight="bold"),
            text_color=ACCENT, fg_color=BG_TEXT,
            corner_radius=4,
        ).pack(side="left", padx=(10, 8), pady=8)

        # Filename
        ctk.CTkLabel(
            row, text=filename,
            font=ctk.CTkFont(family="Segoe UI", size=12),
            text_color=TEXT_PRIMARY, anchor="w",
        ).pack(side="left", fill="x", expand=True)

        # Size
        ctk.CTkLabel(
            row, text=size_str,
            font=ctk.CTkFont(family="Segoe UI", size=10),
            text_color=TEXT_MUTED,
        ).pack(side="right", padx=(0, 8))

        # Delete button
        ctk.CTkButton(
            row, text="X", width=28, height=28,
            font=ctk.CTkFont(family="Segoe UI", size=11, weight="bold"),
            fg_color=DANGER, hover_color=DANGER_HOVER,
            text_color="#ffffff", corner_radius=6,
            command=lambda f=filename: self._delete_document(f),
        ).pack(side="right", padx=(0, 6))

    # ─── Actions ──────────────────────────────────────────────

    def _add_documents(self):
        filetypes = [
            (t("supported_docs"), "*.pdf *.txt *.md *.csv"),
            ("PDF", "*.pdf"),
            (t("text"), "*.txt"),
            ("Markdown", "*.md"),
            ("CSV", "*.csv"),
        ]

        paths = filedialog.askopenfilenames(
            parent=self,
            title=t("select_documents"),
            filetypes=filetypes,
        )

        if not paths:
            return

        for src_path in paths:
            filename = os.path.basename(src_path)
            dst_path = os.path.join(DOCS_FOLDER, filename)
            shutil.copy2(src_path, dst_path)

        self._refresh_list()
        self._on_docs_changed()

    def _delete_document(self, filename: str):
        filepath = os.path.join(DOCS_FOLDER, filename)
        if os.path.exists(filepath):
            os.remove(filepath)
        self._refresh_list()
        self._on_docs_changed()

    # ─── Helpers ──────────────────────────────────────────────

    @staticmethod
    def _get_doc_files() -> list[str]:
        if not os.path.isdir(DOCS_FOLDER):
            return []
        return sorted(
            f for f in os.listdir(DOCS_FOLDER)
            if os.path.splitext(f)[1].lower() in SUPPORTED_EXTENSIONS
        )

    @staticmethod
    def _format_size(size_bytes: int) -> str:
        if size_bytes < 1024:
            return f"{size_bytes} B"
        elif size_bytes < 1024 * 1024:
            return f"{size_bytes / 1024:.1f} KB"
        return f"{size_bytes / (1024 * 1024):.1f} MB"
