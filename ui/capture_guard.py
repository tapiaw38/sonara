import ctypes

from config import IS_WINDOWS


class CaptureGuard:
    """Controla la visibilidad de la ventana ante screen capture."""

    _WDA_NONE = 0x00000000
    _WDA_EXCLUDE = 0x00000011

    def __init__(self, window):
        self._window = window
        self._hwnd = ctypes.windll.user32.GetParent(window.winfo_id()) if IS_WINDOWS else None
        self._hidden = False

    @property
    def is_hidden(self) -> bool:
        return self._hidden

    def set_hidden(self, hidden: bool) -> bool:
        """Activa/desactiva ocultación. Retorna True si tuvo éxito."""
        if not IS_WINDOWS:
            return False
        flag = self._WDA_EXCLUDE if hidden else self._WDA_NONE
        result = ctypes.windll.user32.SetWindowDisplayAffinity(self._hwnd, flag)
        if result:
            self._hidden = hidden
        return bool(result)

    def toggle(self) -> bool:
        return self.set_hidden(not self._hidden)
