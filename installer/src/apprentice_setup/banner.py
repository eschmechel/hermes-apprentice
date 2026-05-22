"""ASCII-art banner for the apprentice-setup installer.

Honeybee/honeycomb motif: Apprentice grows a *zoo* of small specialists from one
warm base — worker bees off a single hive. ANSI colour is applied only when the
output stream is a TTY (and NO_COLOR is unset), so piped/captured output stays
clean.
"""

from __future__ import annotations

import os
import sys

# Palette (matches the dashboard's pollen/sunflower/pumpkin theme).
_POLLEN = "\033[38;5;221m"
_SUNFLOWER = "\033[38;5;214m"
_PUMPKIN = "\033[38;5;208m"
_DIM = "\033[2m"
_RESET = "\033[0m"

_ART = r"""
                 \   /
                  \ /
           bzz     *
              .-=======-.
            .-===========-.
           /===============\
          |=================|
          |=================|
          |======.---.======|
          '------'   '------'

   ___                            _   _
  / _ \                          | | (_)
 / /_\ \ _ __  _ __  _ __ ___ _ _| |_ _  ___ ___
 |  _  || '_ \| '_ \| '__/ _ \ '_ \ __| |/ __/ _ \
 | | | || |_) | |_) | | |  __/ | | | |_| | (_|  __/
 \_| |_/| .__/| .__/|_|  \___|_| |_|\__|_|\___\___|
        | |   | |
        |_|   |_|   s e t u p

   skills are prompts; Apprentice makes some of them weights.
   one warm hive  ·  a zoo of small specialists
"""


def render(*, color: bool | None = None) -> str:
    """Return the banner. ``color`` forces ANSI on/off; None auto-detects a TTY."""
    if color is None:
        color = sys.stdout.isatty() and os.environ.get("NO_COLOR") is None
    if not color:
        return _ART
    # Tint the whole block sunflower; brighten the wordmark/tagline in pollen.
    out = _ART.replace("Apprentice", f"{_POLLEN}Apprentice{_SUNFLOWER}")
    out = out.replace("s e t u p", f"{_PUMPKIN}s e t u p{_SUNFLOWER}")
    return f"{_SUNFLOWER}{out}{_RESET}"


def print_banner(stream=None) -> None:
    (stream or sys.stdout).write(render() + "\n")
