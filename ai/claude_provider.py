import anthropic

from ai.base import AIProvider


class ClaudeProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        client = anthropic.Anthropic(api_key=self._api_key)

        # Claude usa system separado, filtrar del historial
        messages = []
        for entry in chat_history[-20:]:
            if entry["role"] in ("user", "assistant"):
                messages.append(entry)
        messages.append({"role": "user", "content": question})

        response = client.messages.create(
            model=self._model,
            max_tokens=256,
            system=self._build_system_prompt(detected_lang),
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
        content.append({"type": "text", "text": "Analyze this coding problem and provide the solution."})

        messages = []
        for entry in chat_history[-10:]:
            if entry["role"] in ("user", "assistant"):
                messages.append(entry)
        messages.append({"role": "user", "content": content})

        response = client.messages.create(
            model=self._model,
            max_tokens=2048,
            system=self._build_vision_prompt(detected_lang),
            messages=messages,
        )
        return response.content[0].text
