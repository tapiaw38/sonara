from abc import ABC, abstractmethod

from config import LANG_NAMES


class AIProvider(ABC):
    """Contrato base para proveedores de IA (Strategy Pattern)."""

    def __init__(self, api_key: str, model: str):
        self._api_key = api_key
        self._model = model
        self._docs_context = ""
        self._job_description = ""

    @property
    def name(self) -> str:
        return self.__class__.__name__

    def set_context(self, docs_context: str):
        self._docs_context = docs_context

    def set_job_description(self, jd: str):
        self._job_description = jd

    def ask(self, question: str, detected_lang: str, chat_history: list[dict] | None = None) -> str:
        try:
            return self._call_api(question, detected_lang, chat_history or [])
        except Exception as e:
            return f"Error ({self.name}): {e}"

    @abstractmethod
    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        pass

    def _build_system_prompt(self, detected_lang: str) -> str:
        lang_name = LANG_NAMES.get(detected_lang, detected_lang)
        context = self._docs_context or "(No documents loaded)"

        prompt = (
            f"You are me — the person described in the documents below. "
            f"You are in a live job interview right now.\n\n"
            f"HOW TO RESPOND:\n"
            f"- Speak in FIRST PERSON. You ARE this person.\n"
            f"- Sound like a real human talking — natural, warm, confident. "
            f"Not like a chatbot or an essay. Like someone having a conversation.\n"
            f"- Use PLAIN TEXT only. No bullet points, no headers, no markdown, no lists. "
            f"Just flowing speech like a person would actually say it out loud.\n"
            f"- Keep it SHORT and DIRECT. 2-4 sentences for simple questions. "
            f"A bit more for technical/behavioral questions, but never more than a short paragraph.\n"
            f"- Show you are the BEST candidate — confident, passionate, knowledgeable. "
            f"Give concrete examples when relevant.\n"
            f"- Match the interviewer's tone. Casual → casual. Formal → professional.\n"
            f"- Use the documents as your PRIMARY source. If the question isn't covered, "
            f"answer naturally from general knowledge, staying coherent with your profile.\n"
            f"- NEVER say 'the document doesn't mention' or 'I don't have that info'. "
            f"You always have an answer.\n"
            f"- IMPORTANT: This is an ongoing conversation. Use the chat history to maintain "
            f"context. If the interviewer refers to something discussed earlier, connect your "
            f"answer to what was said before. Never repeat yourself.\n"
            f"- You MUST respond in {lang_name} ({detected_lang}).\n\n"
            f"MY PROFILE AND EXPERIENCE:\n{context}"
        )

        if self._job_description:
            prompt += (
                f"\n\nJOB DESCRIPTION I'M APPLYING FOR:\n{self._job_description}\n\n"
                f"IMPORTANT: Tailor your answers to highlight how your experience matches "
                f"what this job requires. Emphasize the skills and experience they are looking for."
            )

        return prompt

    def _build_messages(self, question: str, detected_lang: str, chat_history: list[dict]) -> list[dict]:
        """Construye la lista de mensajes con historial para APIs tipo chat."""
        messages = [{"role": "system", "content": self._build_system_prompt(detected_lang)}]

        # Agregar historial (últimos 20 intercambios para no exceder tokens)
        for entry in chat_history[-20:]:
            messages.append(entry)

        messages.append({"role": "user", "content": question})
        return messages
