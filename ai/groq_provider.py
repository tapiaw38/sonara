from groq import Groq

from ai.base import AIProvider


VISION_MODEL = "meta-llama/llama-4-scout-17b-16e-instruct"


class GroqProvider(AIProvider):

    def _call_api(self, question: str, detected_lang: str, chat_history: list[dict]) -> str:
        client = Groq(api_key=self._api_key)

        response = client.chat.completions.create(
            model=self._model,
            max_tokens=256,
            messages=self._build_messages(question, detected_lang, chat_history),
        )
        return response.choices[0].message.content

    def _call_vision_api(self, images: list[str], detected_lang: str, chat_history: list[dict]) -> str:
        client = Groq(api_key=self._api_key)

        # Groq limita a 5 imágenes, tomar frames distribuidos
        if len(images) > 5:
            step = len(images) / 5
            images = [images[int(i * step)] for i in range(5)]

        content = [{"type": "text", "text": "Analyze this coding problem and provide the solution."}]
        for img_b64 in images:
            content.append({
                "type": "image_url",
                "image_url": {"url": f"data:image/png;base64,{img_b64}", "detail": "high"},
            })

        messages = [{"role": "system", "content": self._build_vision_prompt(detected_lang)}]
        for entry in chat_history[-10:]:
            messages.append(entry)
        messages.append({"role": "user", "content": content})

        response = client.chat.completions.create(
            model=VISION_MODEL,
            max_tokens=2048,
            messages=messages,
        )
        return response.choices[0].message.content
