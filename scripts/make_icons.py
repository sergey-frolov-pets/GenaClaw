#!/usr/bin/env python3
"""Пересобирает все иконки проекта из одного источника — assets/genaclaw.svg.

Что обновляется:

    assets/genaclaw-512.png                       512x512, картинка логотипа
    web/frontend/public/favicon.svg               вектор (без Inkscape-мусора)
    web/frontend/public/favicon.ico               16/32/48
    web/frontend/public/favicon-96x96.png         96x96
    web/frontend/public/apple-touch-icon.png      180x180
    web/frontend/public/web-app-manifest-192x192.png
    web/frontend/public/web-app-manifest-512x512.png
    web/frontend/public/logo_with_text.png        шапка веб-UI: иконка + «GenaClaw»
    web/backend/icon.png                          512x512 (трей / окно)
    web/backend/icon.ico                          16/32/48/256 (Windows-ресурс)
    scripts/icon.icns                             16…1024 (macOS-бандл)

Запуск:

    python3 scripts/make_icons.py           # пересобрать всё
    python3 scripts/make_icons.py --dry-run # показать, что будет записано
"""

from __future__ import annotations

import argparse
import io
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "assets" / "genaclaw.svg"

GREEN = "#146b2c"
RED = "#d65242"
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"

ICO_SMALL = [16, 32, 48]
ICO_APP = [16, 32, 48, 256]
ICNS_SIZES = [16, 32, 64, 128, 256, 512, 1024]


def clean_svg(text: str) -> str:
    """Убирает служебные атрибуты Inkscape/Sodipodi — для веб-ассетов они лишние."""
    text = re.sub(r"<sodipodi:namedview.*?/>", "", text, flags=re.S)
    text = re.sub(r"\s(?:inkscape|sodipodi):[\w-]+=\"[^\"]*\"", "", text)
    text = re.sub(r"\sxmlns:(?:inkscape|sodipodi)=\"[^\"]*\"", "", text)
    text = re.sub(r"(?m)^[ \t]+$\n", "", text)
    return re.sub(r"\n{3,}", "\n\n", text)


def render_master() -> "object":
    """Рендерит источник в 1024x1024 RGBA — из него делаются все размеры."""
    import cairosvg
    from PIL import Image

    png = cairosvg.svg2png(url=str(SOURCE), output_width=1024, output_height=1024)
    return Image.open(io.BytesIO(png)).convert("RGBA")


def save_png(master, size: int, path: Path) -> None:
    from PIL import Image

    img = master if size == master.width else master.resize((size, size), Image.LANCZOS)
    path.parent.mkdir(parents=True, exist_ok=True)
    img.save(path, "PNG", optimize=True)


def save_ico(master, sizes: list[int], path: Path) -> None:
    from PIL import Image

    base = master if master.width == max(sizes) else master.resize((max(sizes), max(sizes)), Image.LANCZOS)
    base.save(path, "ICO", sizes=[(s, s) for s in sizes])


def save_icns(master, path: Path) -> None:
    from PIL import Image

    images = [master if s == 1024 else master.resize((s, s), Image.LANCZOS) for s in sorted(ICNS_SIZES, reverse=True)]
    images[0].save(path, "ICNS", append_images=images[1:])


def save_text_logo(master, path: Path, height: int = 208) -> None:
    """Шапка веб-UI: иконка слева + «Gena» зелёным, «Claw» красным."""
    from PIL import Image, ImageDraw, ImageFont

    font = ImageFont.truetype(FONT, int(height * 0.66))
    draw = ImageDraw.Draw(Image.new("RGBA", (1, 1)))
    gap = int(height * 0.11)
    text_w = int(draw.textlength("GenaClaw", font=font))
    icon = master.resize((height, height), Image.LANCZOS)

    canvas = Image.new("RGBA", (height + gap + text_w + 4, height), (0, 0, 0, 0))
    canvas.alpha_composite(icon, (0, 0))

    draw = ImageDraw.Draw(canvas)
    x = height + gap
    ascent, descent = font.getmetrics()
    baseline = (height - (ascent + descent)) // 2 + ascent
    for part, color in (("Gena", GREEN), ("Claw", RED)):
        draw.text((x, baseline), part, font=font, fill=color, anchor="ls")
        x += int(draw.textlength(part, font=font))
    canvas.save(path, "PNG", optimize=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dry-run", action="store_true", help="только показать список файлов")
    args = parser.parse_args()

    if not SOURCE.exists():
        print(f"нет источника: {SOURCE}")
        return 1

    public = ROOT / "web" / "frontend" / "public"
    backend = ROOT / "web" / "backend"

    png_jobs = [
        (512, ROOT / "assets" / "genaclaw-512.png"),
        (512, backend / "icon.png"),
        (512, public / "web-app-manifest-512x512.png"),
        (192, public / "web-app-manifest-192x192.png"),
        (180, public / "apple-touch-icon.png"),
        (96, public / "favicon-96x96.png"),
    ]

    if args.dry_run:
        for _, path in png_jobs:
            print(path.relative_to(ROOT))
        for path in (public / "favicon.ico", backend / "icon.ico", ROOT / "scripts" / "icon.icns",
                     public / "logo_with_text.png", public / "favicon.svg"):
            print(path.relative_to(ROOT))
        return 0

    master = render_master()
    for size, path in png_jobs:
        save_png(master, size, path)

    save_ico(master, ICO_SMALL, public / "favicon.ico")
    save_ico(master, ICO_APP, backend / "icon.ico")
    save_icns(master, ROOT / "scripts" / "icon.icns")
    save_text_logo(master, public / "logo_with_text.png")

    svg = clean_svg(SOURCE.read_text(encoding="utf-8"))
    (public / "favicon.svg").write_text(svg, encoding="utf-8")

    print(f"готово, источник: {SOURCE.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
