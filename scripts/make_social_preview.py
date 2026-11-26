#!/usr/bin/env python3
"""Собирает assets/social-preview.svg (1280x640, шапка репозитория) из assets/genaclaw.svg.

Иконка встраивается в SVG целиком: градиенты из `<defs>` исходника плюс всё, что стоит
между `</defs>` и `</svg>` (фоновая плашка и слои с путями). Поэтому файл самодостаточен —
его одинаково рисуют GitHub, браузер и cairosvg, без внешних ссылок на PNG.

Скрипт не разбирает внутренности логотипа: поменяли genaclaw.svg — пересобрали шапку.

    python3 scripts/make_social_preview.py            # пересобрать SVG
    python3 scripts/make_social_preview.py --png      # ещё и social-preview.png 1280x640
"""

from __future__ import annotations

import argparse
import re
from pathlib import Path

ASSETS = Path(__file__).resolve().parent.parent / "assets"
SOURCE = ASSETS / "genaclaw.svg"
TARGET = ASSETS / "social-preview.svg"
TARGET_PNG = ASSETS / "social-preview.png"

# Иконка в исходнике 937x937, на шапке занимает 512x512 в левом верхнем углу.
ICON_SIZE = 512
ICON_X, ICON_Y = 64, 64
ICON_SCALE = ICON_SIZE / 937


def extract_icon(svg: str) -> tuple[str, str]:
    """Возвращает (содержимое <defs>, всё после </defs> до </svg>) исходной иконки."""
    defs = re.search(r"<defs[^>]*>(.*?)</defs>", svg, re.S)
    if not defs:
        raise SystemExit("в genaclaw.svg не найден блок <defs>")

    end = svg.rfind("</svg>")
    if end == -1:
        raise SystemExit("в genaclaw.svg не найден закрывающий </svg>")
    body = svg[defs.end() : end].strip()
    if not body:
        raise SystemExit("в genaclaw.svg нет содержимого после </defs>")

    return defs.group(1).strip(), body


TEMPLATE = """<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 640" width="1280" height="640"
     xmlns:inkscape="http://www.inkscape.org/namespaces/inkscape"
     xmlns:sodipodi="http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd"
     role="img" aria-label="GenaClaw — a working fork of PicoClaw">
  <title>GenaClaw</title>
  <defs>
{icon_defs}
    <linearGradient id="page" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%" stop-color="#0d3a1b"/>
      <stop offset="55%" stop-color="#12522a"/>
      <stop offset="100%" stop-color="#146B2C"/>
    </linearGradient>
    <linearGradient id="glow" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="#7ee2a8" stop-opacity="0.28"/>
      <stop offset="100%" stop-color="#7ee2a8" stop-opacity="0"/>
    </linearGradient>
    <clipPath id="iconslot"><rect x="{x}" y="{y}" width="{size}" height="{size}" rx="96"/></clipPath>
  </defs>

  <rect width="1280" height="640" fill="url(#page)"/>
  <ellipse cx="320" cy="120" rx="420" ry="300" fill="url(#glow)"/>

  <g clip-path="url(#iconslot)">
    <g transform="translate({x},{y}) scale({scale:.6f})">
      {icon_body}
    </g>
  </g>

  <g font-family="DejaVu Sans, Verdana, sans-serif" fill="#ffffff">
    <text x="632" y="248" font-size="104" font-weight="bold" letter-spacing="-1">GenaClaw</text>
    <text x="634" y="312" font-size="34" fill="#b9e7c9">a working fork of PicoClaw</text>

    <g font-size="27" fill="#e8f5ec">
      <text x="634" y="392">Turn memory, honest long sessions,</text>
      <text x="634" y="432">structured UI in every channel.</text>
    </g>

    <rect x="634" y="470" width="560" height="2" fill="#ffffff" opacity="0.18"/>
    <text x="634" y="522" font-size="24" fill="#9fd9b3">Go · MIT · self-hosted · sipeed/picoclaw</text>
    <text x="634" y="560" font-size="24" fill="#9fd9b3">github.com/sergey-frolov-pets/GenaClaw</text>
  </g>
</svg>
"""


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--png", action="store_true", help="отрендерить social-preview.png 1280x640")
    args = parser.parse_args()

    icon_defs, icon_body = extract_icon(SOURCE.read_text(encoding="utf-8"))
    TARGET.write_text(
        TEMPLATE.format(
            icon_defs=icon_defs,
            icon_body=icon_body,
            x=ICON_X,
            y=ICON_Y,
            size=ICON_SIZE,
            scale=ICON_SCALE,
        ),
        encoding="utf-8",
    )
    print(f"написан {TARGET.relative_to(TARGET.parent.parent)}")

    if args.png:
        try:
            import cairosvg  # type: ignore
        except ImportError:
            print("cairosvg не установлен — PNG не сделан")
            return 1
        cairosvg.svg2png(url=str(TARGET), write_to=str(TARGET_PNG),
                         output_width=1280, output_height=640)
        print(f"написан {TARGET_PNG.relative_to(TARGET.parent.parent)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
