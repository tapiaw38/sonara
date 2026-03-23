from openai import OpenAI

from ai.base import AIProvider


class OpenAIProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str) -> str:
        client = OpenAI(api_key=self._api_key)

        response = client.chat.completions.create(
            model=self._model,
            max_tokens=256,
            messages=[
                {"role": "system", "content": self._build_system_prompt(detected_lang)},
                {"role": "user", "content": question},
            ],
        )
        return response.choices[0].message.content
