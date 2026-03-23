from ai.base import AIProvider
from config import API_KEYS, AI_MODELS


def create_ai_provider(provider_name: str) -> AIProvider:
    """Factory: crea el proveedor de IA según el nombre."""

    api_key = API_KEYS.get(provider_name)
    model = AI_MODELS.get(provider_name)

    if not api_key:
        raise ValueError(f"No hay API key para '{provider_name}'. Configúrala en config.py")

    if provider_name == "gemini":
        from ai.gemini_provider import GeminiProvider
        return GeminiProvider(api_key, model)

    elif provider_name == "claude":
        from ai.claude_provider import ClaudeProvider
        return ClaudeProvider(api_key, model)

    elif provider_name == "openai":
        from ai.openai_provider import OpenAIProvider
        return OpenAIProvider(api_key, model)

    elif provider_name == "groq":
        from ai.groq_provider import GroqProvider
        return GroqProvider(api_key, model)

    raise ValueError(f"Proveedor '{provider_name}' no soportado. Usa: gemini, claude, openai, groq")
