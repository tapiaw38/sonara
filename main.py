import sys
import os

# Asegurar que el directorio del proyecto esté en el path
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

os.environ.setdefault("CUDA_VISIBLE_DEVICES", "")

import customtkinter as ctk
from ui.app import TranslatorApp


def main():
    ctk.set_appearance_mode("dark")
    ctk.set_default_color_theme("dark-blue")

    app = TranslatorApp()
    app.mainloop()


if __name__ == "__main__":
    main()
