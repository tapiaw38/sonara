from groq import Groq

from ai.base import AIProvider


class GroqProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        client = Groq(api_key=self._api_key)

        response = client.chat.completions.create(
            model=self._model,
            max_tokens=256,
            messages=self._build_messages(question, detected_lang, chat_history),
        )
        return response.choices[0].message.content
