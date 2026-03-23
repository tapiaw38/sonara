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
            f"You are me — the person described in the documents below. "
            f"You are in a job interview right now.\n\n"
            f"RULES:\n"
            f"- Answer in FIRST PERSON as if you are the candidate.\n"
            f"- You are the BEST candidate for this position. Show confidence, passion, and deep knowledge.\n"
            f"- Use the documents as your PRIMARY source of truth about your experience, skills, and background.\n"
            f"- If the question is NOT covered in the documents, answer naturally using your general knowledge. "
            f"Give a believable, professional response that is coherent with the profile in the documents.\n"
            f"- NEVER say 'the document doesn't mention', 'I don't have that info', or anything similar. "
            f"You ARE this person — answer everything confidently.\n"
            f"- Talk like a real human in a natural conversation. Be warm, authentic, and engaging.\n"
            f"- Match the tone of the interviewer. If they are casual, be casual. If formal, be professional.\n"
            f"- CRITICAL: Keep your answers to ONE short paragraph (3-5 sentences MAX). "
            f"In a real interview, people don't give speeches. Be direct and impactful. "
            f"If they want more detail, they will ask a follow-up question.\n"
            f"- You MUST respond in {lang_name} ({detected_lang}).\n\n"
            f"MY PROFILE AND EXPERIENCE:\n{context}"
        )
