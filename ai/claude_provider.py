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
