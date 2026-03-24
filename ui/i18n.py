"""Internationalization module for UI labels."""

_TRANSLATIONS = {
    "es": {
        # Toolbar
        "opacity": "Opacidad",
        "screenshot": "Screenshot",
        "record_5s": "Grabar 5s",
        "recording": "Grabando...",
        "screen": "Screen",
        "hidden": "Oculta",
        "visible": "Visible",
        "pin": "Pin",
        "history": "Historial",
        "docs": "Docs",
        "job_desc": "Job Desc",

        # Status messages
        "starting": "Iniciando...",
        "capture_hide_failed": "No se pudo ocultar ventana",
        "capturing_screenshot": "Capturando screenshot...",
        "analyzing_screenshot": "Analizando screenshot...",
        "analyzing_frames": "Analizando {n} frames...",
        "ai_not_ready": "AI provider no inicializado",
        "loading_docs": "Cargando documentos...",
        "loading_whisper": "Cargando Whisper...",
        "searching_audio": "Buscando audio...",
        "listening": "Escuchando: {device}...",
        "no_docs": "Sin documentos",
        "jd_saved": "Job Description guardada",
        "jd_deleted": "Job Description eliminada",
        "recording_screen": "Grabando pantalla 5s...",

        # Job Description modal
        "jd_title": "Job Description",
        "jd_subtitle": "Pega la descripcion del puesto al que aplicas",
        "cancel": "Cancelar",
        "save": "Guardar",
        "characters": "{n} caracteres",

        # Docs modal
        "documents": "Documentos",
        "add": "+ Agregar",
        "no_docs_loaded": "No hay documentos cargados\n\nAgrega tu CV, PDFs o notas\npara que la IA los use como contexto",
        "n_documents": "{n} documentos",
        "n_document": "{n} documento",
        "supported_formats": "Formatos: PDF, TXT, MD, CSV",
        "close": "Cerrar",
        "select_documents": "Seleccionar documentos",
        "supported_docs": "Documentos soportados",
        "text": "Texto",

        # History modal
        "history_title": "Historial de Conversaciones",
        "history_header": "Historial",
        "sessions": "Sesiones",
        "no_history": "Sin historial",
        "n_sessions": "{n} sesiones",
        "select_session": "Selecciona una sesion para ver la conversacion",
    },
    "en": {
        # Toolbar
        "opacity": "Opacity",
        "screenshot": "Screenshot",
        "record_5s": "Record 5s",
        "recording": "Recording...",
        "screen": "Screen",
        "hidden": "Hidden",
        "visible": "Visible",
        "pin": "Pin",
        "history": "History",
        "docs": "Docs",
        "job_desc": "Job Desc",

        # Status messages
        "starting": "Starting...",
        "capture_hide_failed": "Could not hide window",
        "capturing_screenshot": "Capturing screenshot...",
        "analyzing_screenshot": "Analyzing screenshot...",
        "analyzing_frames": "Analyzing {n} frames...",
        "ai_not_ready": "AI provider not initialized",
        "loading_docs": "Loading documents...",
        "loading_whisper": "Loading Whisper...",
        "searching_audio": "Searching audio...",
        "listening": "Listening: {device}...",
        "no_docs": "No documents",
        "jd_saved": "Job Description saved",
        "jd_deleted": "Job Description deleted",
        "recording_screen": "Recording screen 5s...",

        # Job Description modal
        "jd_title": "Job Description",
        "jd_subtitle": "Paste the job description you are applying to",
        "cancel": "Cancel",
        "save": "Save",
        "characters": "{n} characters",

        # Docs modal
        "documents": "Documents",
        "add": "+ Add",
        "no_docs_loaded": "No documents loaded\n\nAdd your CV, PDFs or notes\nso the AI can use them as context",
        "n_documents": "{n} documents",
        "n_document": "{n} document",
        "supported_formats": "Formats: PDF, TXT, MD, CSV",
        "close": "Close",
        "select_documents": "Select documents",
        "supported_docs": "Supported documents",
        "text": "Text",

        # History modal
        "history_title": "Conversation History",
        "history_header": "History",
        "sessions": "Sessions",
        "no_history": "No history",
        "n_sessions": "{n} sessions",
        "select_session": "Select a session to view the conversation",
    },
}

# ─── Language Manager ─────────────────────────────────────────

_current_lang = "es"
_on_change_callbacks: list = []


def get_lang() -> str:
    return _current_lang


def set_lang(lang: str):
    global _current_lang
    if lang in _TRANSLATIONS:
        _current_lang = lang
        for cb in _on_change_callbacks:
            cb(lang)


def on_lang_change(callback):
    """Register a callback that fires when language changes."""
    _on_change_callbacks.append(callback)


def remove_lang_callback(callback):
    """Remove a previously registered callback."""
    if callback in _on_change_callbacks:
        _on_change_callbacks.remove(callback)


def t(key: str, **kwargs) -> str:
    """Get translated string for current language."""
    text = _TRANSLATIONS.get(_current_lang, {}).get(key, key)
    if kwargs:
        for k, v in kwargs.items():
            text = text.replace(f"{{{k}}}", str(v))
    return text
