from deep_translator import GoogleTranslator


class TextTranslator:
    """Traduce texto entre idiomas usando Google Translate."""

    def __init__(self, target_lang: str):
        self._target_lang = target_lang
        self._translator = GoogleTranslator(source="auto", target=target_lang)

    @property
    def target_lang(self) -> str:
        return self._target_lang

    def translate(self, text: str) -> str | None:
        """Traduce texto. Retorna None si falla."""
        try:
            return self._translator.translate(text)
        except Exception:
            return None

    def needs_translation(self, source_lang: str) -> bool:
        return source_lang != self._target_lang
