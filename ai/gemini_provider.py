import google.generativeai as genai

from ai.base import AIProvider


class GeminiProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str) -> str:
        genai.configure(api_key=self._api_key)

        model = genai.GenerativeModel(
            model_name=self._model,
            system_instruction=self._build_system_prompt(detected_lang),
        )
        response = model.generate_content(question)
        return response.text
