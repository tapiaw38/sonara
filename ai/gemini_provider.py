import base64

import google.generativeai as genai

from ai.base import AIProvider


class GeminiProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        genai.configure(api_key=self._api_key)

        model = genai.GenerativeModel(
            model_name=self._model,
            system_instruction=self._build_system_prompt(detected_lang),
        )

        # Convertir historial al formato de Gemini
        gemini_history = []
        for entry in chat_history[-20:]:
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
        parts.append("Analyze this coding problem and provide the solution.")

        gemini_history = []
        for entry in chat_history[-10:]:
            role = "user" if entry["role"] == "user" else "model"
            gemini_history.append({"role": role, "parts": [entry["content"]]})

        chat = model.start_chat(history=gemini_history)
        response = chat.send_message(parts)
        return response.text
