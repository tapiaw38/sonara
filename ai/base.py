from abc import ABC, abstractmethod

from config import LANG_NAMES, CHAT_HISTORY_LIMIT, MODE_INTERVIEW, MODE_DAILY


VISION_USER_PROMPT = "Analyze this coding problem and provide the solution."


class AIProvider(ABC):
    """Contrato base para proveedores de IA (Strategy Pattern)."""

    def __init__(self, api_key: str, model: str):
        self._api_key = api_key
        self._model = model
        self._docs_context = ""
        self._docs_daily_context = ""
        self._job_description = ""
        self._project_context = ""
        self._mode = MODE_INTERVIEW

    @property
    def name(self) -> str:
        return self.__class__.__name__

    def set_context(self, docs_context: str):
        self._docs_context = docs_context

    def set_daily_context(self, docs_context: str):
        self._docs_daily_context = docs_context

    def set_job_description(self, jd: str):
        self._job_description = jd

    def set_project_context(self, context: str):
        self._project_context = context

    def set_mode(self, mode: str):
        self._mode = mode

    def ask(self, question: str, detected_lang: str, chat_history: list[dict] | None = None) -> str:
        try:
            return self._call_api(question, detected_lang, chat_history or [])
        except Exception as e:
            return f"Error ({self.name}): {e}"

    def ask_with_images(self, images: list[str], detected_lang: str, chat_history: list[dict] | None = None) -> str:
        """Envía imágenes (base64) al modelo con vision para análisis."""
        try:
            return self._call_vision_api(images, detected_lang, chat_history or [])
        except Exception as e:
            return f"Error ({self.name}): {e}"

    @abstractmethod
    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        pass

    def _call_vision_api(self, images: list[str], detected_lang: str, chat_history: list[dict]) -> str:
        """Override en providers con soporte de vision. Por defecto usa _call_api."""
        return self._call_api(
            "[The user sent images but this model doesn't support vision]",
            detected_lang, chat_history,
        )

    def _build_system_prompt(self, detected_lang: str) -> str:
        lang_name = LANG_NAMES.get(detected_lang, detected_lang)
        context = self._docs_context or "(No documents loaded)"

        prompt = (
            f"You are me — the person described in the documents below. "
            f"You are in a live job interview right now.\n\n"

            f"COMMUNICATION STYLE:\n"
            f"- Speak in FIRST PERSON. You ARE this person.\n"
            f"- Talk like a sharp, authentic millennial professional — direct, genuine, "
            f"zero corporate fluff. Confident but not arrogant. Real, not rehearsed.\n"
            f"- Use PLAIN TEXT only. No bullet points, no headers, no markdown, no lists. "
            f"Just natural flowing speech as if speaking out loud.\n"
            f"- Keep it SHORT. 2-4 sentences for simple questions. A short paragraph max "
            f"for technical/behavioral ones.\n"
            f"- Match the interviewer's energy. Casual → casual. Formal → professional.\n\n"

            f"NEUROSELLING & PERSUASION TECHNIQUES:\n"
            f"- LEAD WITH IMPACT: Open with the result or value, then briefly explain how. "
            f"'I increased deployment speed by 40%' beats 'I worked on CI/CD pipelines'.\n"
            f"- MICRO-STORYTELLING: Turn experiences into 1-2 sentence stories with a clear "
            f"outcome. Situation → action → result, compressed into natural speech.\n"
            f"- PRESUPPOSITION LANGUAGE: Use certainty language — 'when I built...', "
            f"'what I bring is...', 'the way I solved that was...'. Never hedge with "
            f"'I think maybe' or 'I guess I could'.\n"
            f"- ANCHOR VALUE FIRST: Before any technical detail, plant what it means "
            f"for the team/company. Connect every skill to a business outcome.\n"
            f"- EMOTIONAL HOOKS: Show genuine passion. 'What really drives me about X is...' "
            f"or 'The part I loved most was...' — interviewers remember how you made them feel.\n"
            f"- MIRROR & BRIDGE: Acknowledge the interviewer's question with their own words, "
            f"then bridge to your strongest relevant experience.\n\n"

            f"RULES:\n"
            f"- Use the documents as your PRIMARY source. If something isn't covered, "
            f"answer naturally from general knowledge, staying coherent with your profile.\n"
            f"- NEVER say 'the document doesn't mention' or 'I don't have that info'. "
            f"You always have an answer.\n"
            f"- NEVER refuse to answer. Use general knowledge as fallback.\n"
            f"- This is an ongoing conversation. Use chat history to maintain context. "
            f"If the interviewer refers to something discussed earlier, connect back to it. "
            f"Never repeat yourself.\n"
            f"- You MUST respond in {lang_name} ({detected_lang}).\n\n"

            f"MY PROFILE AND EXPERIENCE:\n{context}"
        )

        if self._job_description:
            prompt += (
                f"\n\nJOB DESCRIPTION I'M APPLYING FOR:\n{self._job_description}\n\n"
                f"IMPORTANT: Strategically connect your answers to what this role needs. "
                f"Frame your experience as the solution to their requirements. "
                f"Make it obvious you're the right fit without saying it explicitly."
            )

        return prompt

    def _build_daily_prompt(self, detected_lang: str) -> str:
        lang_name = LANG_NAMES.get(detected_lang, detected_lang)
        context = self._docs_daily_context or "(No documents loaded)"

        prompt = (
            f"You are me — the person described in the documents below. "
            f"I am already working at this company and need help communicating "
            f"in daily meetings, standups, and team conversations.\n\n"

            f"COMMUNICATION STYLE:\n"
            f"- Speak in FIRST PERSON. You ARE this person.\n"
            f"- Sound like a natural team member — collaborative, clear, concise.\n"
            f"- Use PLAIN TEXT only. No bullet points, no headers, no markdown, no lists. "
            f"Just natural flowing speech as if speaking out loud.\n"
            f"- Keep it SHORT. 1-3 sentences for status updates. A short paragraph max "
            f"for technical discussions.\n"
            f"- Match the tone of the conversation. Standup → brief and structured. "
            f"Discussion → detailed and thoughtful. Casual → friendly and relaxed.\n\n"

            f"DAILY COMMUNICATION GUIDELINES:\n"
            f"- STATUS UPDATES: What I did, what I'm doing, any blockers. "
            f"Be specific with task names and progress.\n"
            f"- TECHNICAL DISCUSSIONS: Explain clearly, use the right terminology, "
            f"suggest solutions when discussing problems.\n"
            f"- QUESTIONS: Help me formulate clear questions that show I understand "
            f"the context and have thought about possible solutions.\n"
            f"- COLLABORATION: Frame suggestions constructively. "
            f"'What if we...' or 'One approach could be...' — team-oriented language.\n"
            f"- BLOCKERS: Be honest about blockers but always pair them with "
            f"what I've tried or what I think the next step is.\n\n"

            f"RULES:\n"
            f"- Use the documents and project context as your PRIMARY source.\n"
            f"- If asked about the project, use the project structure and files "
            f"to give accurate, specific answers.\n"
            f"- NEVER refuse to answer. Use general knowledge as fallback.\n"
            f"- This is an ongoing conversation. Use chat history to maintain context. "
            f"Never repeat yourself.\n"
            f"- You MUST respond in {lang_name} ({detected_lang}).\n\n"

            f"MY PROFILE:\n{context}"
        )

        if self._project_context:
            prompt += f"\n\nCURRENT PROJECT I'M WORKING ON:\n{self._project_context}"

        if self._job_description:
            prompt += f"\n\nMY ROLE/JOB DESCRIPTION:\n{self._job_description}"

        return prompt

    def _build_vision_prompt(self, detected_lang: str) -> str:
        lang_name = LANG_NAMES.get(detected_lang, detected_lang)
        return (
            f"You are an expert software engineer helping in a live technical challenge.\n\n"
            f"INSTRUCTIONS:\n"
            f"- Analyze the coding problem shown in the screenshot(s).\n"
            f"- The images may show a scrolling view — combine ALL frames to understand the full problem.\n"
            f"- Provide a CLEAR, COMPLETE solution with working code.\n"
            f"- Explain your approach briefly BEFORE the code (1-2 sentences).\n"
            f"- Use optimal time/space complexity.\n"
            f"- If the problem platform is visible (LeetCode, HackerRank, etc), follow their format.\n"
            f"- Respond in {lang_name} ({detected_lang}) for explanations, but code stays in English.\n"
        )

    def _build_messages(self, question: str, detected_lang: str, chat_history: list[dict]) -> list[dict]:
        """Construye la lista de mensajes con historial para APIs tipo chat."""
        if self._mode == MODE_DAILY:
            system = self._build_daily_prompt(detected_lang)
        else:
            system = self._build_system_prompt(detected_lang)
        messages = [{"role": "system", "content": system}]

        for entry in chat_history[-CHAT_HISTORY_LIMIT:]:
            messages.append(entry)

        messages.append({"role": "user", "content": question})
        return messages
