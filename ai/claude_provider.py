import anthropic

from ai.base import AIProvider, VISION_USER_PROMPT
from config import (
    MAX_TOKENS_TEXT, MAX_TOKENS_VISION,
    CHAT_HISTORY_LIMIT, VISION_HISTORY_LIMIT,
    MODE_DAILY,
)


class ClaudeProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        client = anthropic.Anthropic(api_key=self._api_key)

        messages = []
        for entry in chat_history[-CHAT_HISTORY_LIMIT:]:
            if entry["role"] in ("user", "assistant"):
                messages.append(entry)
        messages.append({"role": "user", "content": question})

        response = client.messages.create(
            model=self._model,
            max_tokens=MAX_TOKENS_TEXT,
            system=(self._build_daily_prompt(detected_lang) if self._mode == MODE_DAILY
                    else self._build_system_prompt(detected_lang)),
            messages=messages,
        )
        return response.content[0].text

    def _call_vision_api(self, images: list[str], detected_lang: str, chat_history: list[dict]) -> str:
        client = anthropic.Anthropic(api_key=self._api_key)

        content = []
        for img_b64 in images:
            content.append({
                "type": "image",
                "source": {"type": "base64", "media_type": "image/png", "data": img_b64},
            })
        content.append({"type": "text", "text": VISION_USER_PROMPT})

        messages = []
        for entry in chat_history[-VISION_HISTORY_LIMIT:]:
            if entry["role"] in ("user", "assistant"):
                messages.append(entry)
        messages.append({"role": "user", "content": content})

        response = client.messages.create(
            model=self._model,
            max_tokens=MAX_TOKENS_VISION,
            system=self._build_vision_prompt(detected_lang),
            messages=messages,
        )
        return response.content[0].text
