import os
import fitz


class DocumentLoader:
    """Carga y extrae texto de documentos (PDF, TXT, MD, CSV)."""

    SUPPORTED_TEXT = (".txt", ".md", ".csv")
    SUPPORTED_PDF = (".pdf",)

    def __init__(self, docs_folder: str):
        self._docs_folder = docs_folder
        self._ensure_folder()

    def _ensure_folder(self):
        if not os.path.exists(self._docs_folder):
            os.makedirs(self._docs_folder)

    def load_all(self) -> tuple[str, list[str]]:
        """Carga todos los documentos. Retorna (contexto, lista_de_archivos)."""
        texts = []
        loaded = []

        for filename in os.listdir(self._docs_folder):
            filepath = os.path.join(self._docs_folder, filename)
            text = self._extract_text(filename, filepath)

            if text:
                texts.append(f"=== {filename} ===\n{text}")
                loaded.append(filename)

        context = "\n\n".join(texts)
        return context, loaded

    def _extract_text(self, filename: str, filepath: str) -> str | None:
        ext = os.path.splitext(filename)[1].lower()

        if ext in self.SUPPORTED_PDF:
            return self._read_pdf(filepath)
        elif ext in self.SUPPORTED_TEXT:
            return self._read_text(filepath)
        return None

    def _read_pdf(self, filepath: str) -> str | None:
        try:
            doc = fitz.open(filepath)
            text = "".join(page.get_text() for page in doc)
            doc.close()
            return text.strip() or None
        except Exception:
            return None

    def _read_text(self, filepath: str) -> str | None:
        try:
            with open(filepath, "r", encoding="utf-8", errors="ignore") as f:
                text = f.read()
            return text.strip() or None
        except Exception:
            return None
