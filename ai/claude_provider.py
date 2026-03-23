import anthropic

from ai.base import AIProvider


class ClaudeProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str) -> str:
        client = anthropic.Anthropic(api_key=self._api_key)

        response = client.messages.create(
            model=self._model,
            max_tokens=1024,
            system=self._build_system_prompt(detected_lang),
            messages=[{"role": "user", "content": question}],
        )
        return response.content[0].text
