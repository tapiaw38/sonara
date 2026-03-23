from abc import ABC, abstractmethod

from config import LANG_NAMES


class AIProvider(ABC):
    """Contrato base para proveedores de IA (Strategy Pattern)."""

    def __init__(self, api_key: str, model: str):
        self._api_key = api_key
        self._model = model
        self._docs_context = ""

    @property
    def name(self) -> str:
        return self.__class__.__name__

    def set_context(self, docs_context: str):
        self._docs_context = docs_context

    def ask(self, question: str, detected_lang: str) -> str:
        try:
            return self._call_api(question, detected_lang)
        except Exception as e:
            return f"Error ({self.name}): {e}"

    @abstractmethod
    def _call_api(self, question: str, detected_lang: str) -> str:
        pass

    def _build_system_prompt(self, detected_lang: str) -> str:
        lang_name = LANG_NAMES.get(detected_lang, detected_lang)
        context = self._docs_context or "(No documents loaded)"

        return (
            f"You are a helpful assistant. Answer questions based on the provided document context.\n"
            f"You MUST respond in {lang_name} ({detected_lang}).\n"
            f"Be concise and direct. If the answer is not in the documents, say so briefly.\n\n"
            f"DOCUMENT CONTEXT:\n{context}"
        )
