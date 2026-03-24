from groq import Groq

from ai.base import AIProvider, VISION_USER_PROMPT
from config import (
    MAX_TOKENS_TEXT, MAX_TOKENS_VISION,
    VISION_HISTORY_LIMIT, MAX_VISION_FRAMES,
)


VISION_MODEL = "meta-llama/llama-4-scout-17b-16e-instruct"


class GroqProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        client = Groq(api_key=self._api_key)

        response = client.chat.completions.create(
            model=self._model,
            max_tokens=MAX_TOKENS_TEXT,
            messages=self._build_messages(question, detected_lang, chat_history),
        )
        return response.choices[0].message.content

    def _call_vision_api(self, images: list[str], detected_lang: str, chat_history: list[dict]) -> str:
        client = Groq(api_key=self._api_key)

        if len(images) > MAX_VISION_FRAMES:
            step = len(images) / MAX_VISION_FRAMES
            images = [images[int(i * step)] for i in range(MAX_VISION_FRAMES)]

        content = [{"type": "text", "text": VISION_USER_PROMPT}]
        for img_b64 in images:
            content.append({
                "type": "image_url",
                "image_url": {"url": f"data:image/png;base64,{img_b64}", "detail": "high"},
            })

        messages = [{"role": "system", "content": self._build_vision_prompt(detected_lang)}]
        for entry in chat_history[-VISION_HISTORY_LIMIT:]:
            messages.append(entry)
        messages.append({"role": "user", "content": content})

        response = client.chat.completions.create(
            model=VISION_MODEL,
            max_tokens=MAX_TOKENS_VISION,
            messages=messages,
        )
        return response.choices[0].message.content
