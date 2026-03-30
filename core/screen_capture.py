import io
import base64
import time
import subprocess
import shutil

import mss
from PIL import Image


class ScreenCapture:
    """Captura screenshots y graba frames de pantalla."""

    MAX_WIDTH = 1280

    def screenshot(self) -> str:
        """Captura un screenshot y retorna base64 PNG."""
        img = self._grab_frame()
        if img is None:
            raise RuntimeError("No se pudo capturar la pantalla")
        return self._to_base64(img)

    def record_frames(self, duration: int = 5, fps: int = 2) -> list[str]:
        """Graba frames por N segundos y retorna lista de base64 PNG."""
        frames = []
        interval = 1.0 / fps
        end_time = time.time() + duration

        while time.time() < end_time:
            start = time.time()
            img = self._grab_frame()
            if img:
                frames.append(self._to_base64(img))
            elapsed = time.time() - start
            if elapsed < interval:
                time.sleep(interval - elapsed)

        return self._deduplicate(frames)

    def _grab_frame(self) -> Image.Image | None:
        """Intenta capturar con mss, si falla usa grim (Wayland) o scrot (X11)."""
        try:
            with mss.mss() as sct:
                img = sct.grab(sct.monitors[1])
                return Image.frombytes("RGB", img.size, img.bgra, "raw", "BGRX")
        except Exception:
            pass

        if shutil.which("grim"):
            try:
                result = subprocess.run(
                    ["grim", "-"],
                    capture_output=True,
                    timeout=5,
                )
                if result.returncode == 0:
                    return Image.open(io.BytesIO(result.stdout)).convert("RGB")
            except Exception:
                pass

        if shutil.which("scrot"):
            try:
                result = subprocess.run(
                    ["scrot", "-"],
                    capture_output=True,
                    timeout=5,
                )
                if result.returncode == 0:
                    return Image.open(io.BytesIO(result.stdout)).convert("RGB")
            except Exception:
                pass

        return None

    def _to_base64(self, img: Image.Image) -> str:
        """Redimensiona si es muy grande y convierte a base64."""
        if img.width > self.MAX_WIDTH:
            ratio = self.MAX_WIDTH / img.width
            img = img.resize((self.MAX_WIDTH, int(img.height * ratio)), Image.LANCZOS)

        buf = io.BytesIO()
        img.save(buf, format="PNG", optimize=True)
        return base64.b64encode(buf.getvalue()).decode()

    @staticmethod
    def _deduplicate(frames: list[str]) -> list[str]:
        """Elimina frames duplicados consecutivos."""
        if not frames:
            return frames
        unique = [frames[0]]
        for f in frames[1:]:
            if f != unique[-1]:
                unique.append(f)
        return unique
