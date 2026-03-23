from groq import Groq

from ai.base import AIProvider


class GroqProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str) -> str:
        client = Groq(api_key=self._api_key)

        response = client.chat.completions.create(
            model=self._model,
            max_tokens=256,
            messages=[
                {"role": "system", "content": self._build_system_prompt(detected_lang)},
                {"role": "user", "content": question},
            ],
        )
        return response.choices[0].message.content
