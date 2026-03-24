import base64

import google.generativeai as genai

from ai.base import AIProvider, VISION_USER_PROMPT
from config import CHAT_HISTORY_LIMIT, VISION_HISTORY_LIMIT


class GeminiProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        genai.configure(api_key=self._api_key)

        model = genai.GenerativeModel(
            model_name=self._model,
            system_instruction=self._build_system_prompt(detected_lang),
        )

        gemini_history = []
        for entry in chat_history[-CHAT_HISTORY_LIMIT:]:
            role = "user" if entry["role"] == "user" else "model"
            gemini_history.append({"role": role, "parts": [entry["content"]]})

        chat = model.start_chat(history=gemini_history)
        response = chat.send_message(question)
        return response.text

    def _call_vision_api(self, images: list[str], detected_lang: str, chat_history: list[dict]) -> str:
        genai.configure(api_key=self._api_key)

        model = genai.GenerativeModel(
            model_name=self._model,
            system_instruction=self._build_vision_prompt(detected_lang),
        )

        parts = []
        for img_b64 in images:
            parts.append({"mime_type": "image/png", "data": base64.b64decode(img_b64)})
        parts.append(VISION_USER_PROMPT)

        gemini_history = []
        for entry in chat_history[-VISION_HISTORY_LIMIT:]:
            role = "user" if entry["role"] == "user" else "model"
            gemini_history.append({"role": role, "parts": [entry["content"]]})

        chat = model.start_chat(history=gemini_history)
        response = chat.send_message(parts)
        return response.text
