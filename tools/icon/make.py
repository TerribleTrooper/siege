#!/usr/bin/env python3
"""Иконка «Осады»: зубчатая стена с воротами.

Рисуем, а не храним: иконка нужна в трёх размерах, и держать их в репозитории
как бинарники незачем. Содержимое вписано в центральные 70% полотна — на
Android систему вольна обрезать иконку по кругу (maskable), и углы она съест.

    python3 tools/icon/make.py          # обновить web/icon-*.png и web/icon.svg
"""

import os
from PIL import Image, ImageDraw

BG = (27, 23, 17)        # как фон страницы, но чуть светлее: на тёмном столе
GOLD = (216, 169, 63)    # var(--accent)
S = 512

WALL_L, WALL_R = 136, 376
WALL_TOP, WALL_BOT = 210, 368
MERLON_TOP = 168
GATE_L, GATE_R = 228, 284
GATE_TOP, GATE_BOT = 292, 368


def draw(d):
    d.rectangle([WALL_L, WALL_TOP, WALL_R, WALL_BOT], fill=GOLD)
    # Зубцы: пять на пролёт, между ними просветы.
    w, gap = 32, 20
    x = WALL_L
    while x + w <= WALL_R:
        d.rectangle([x, MERLON_TOP, x + w, WALL_TOP], fill=GOLD)
        x += w + gap
    # Ворота: арка, вырезанная фоном.
    r = (GATE_R - GATE_L) // 2
    d.rectangle([GATE_L, GATE_TOP, GATE_R, GATE_BOT], fill=BG)
    d.ellipse([GATE_L, GATE_TOP - r, GATE_R, GATE_TOP + r], fill=BG)
    # Ров под стеной — та же полоска, что на поле.
    d.rectangle([WALL_L - 24, WALL_BOT + 22, WALL_R + 24, WALL_BOT + 40], fill=GOLD)


SVG = f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {S} {S}">
<rect width="{S}" height="{S}" fill="rgb{BG}"/>
<g fill="rgb{GOLD}">
<rect x="{WALL_L}" y="{WALL_TOP}" width="{WALL_R - WALL_L}" height="{WALL_BOT - WALL_TOP}"/>
{chr(10).join(f'<rect x="{x}" y="{MERLON_TOP}" width="32" height="{WALL_TOP - MERLON_TOP}"/>' for x in range(WALL_L, WALL_R - 31, 52))}
<rect x="{WALL_L - 24}" y="{WALL_BOT + 22}" width="{WALL_R - WALL_L + 48}" height="18"/>
</g>
<path d="M{GATE_L} {GATE_BOT}V{GATE_TOP}a{(GATE_R - GATE_L) // 2} {(GATE_R - GATE_L) // 2} 0 0 1 {GATE_R - GATE_L} 0v{GATE_BOT - GATE_TOP}z" fill="rgb{BG}"/>
</svg>
"""


def main():
    out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "web")
    img = Image.new("RGB", (S, S), BG)
    draw(ImageDraw.Draw(img))
    for size, name in ((512, "icon-512.png"), (192, "icon-192.png"), (180, "icon-180.png")):
        path = os.path.join(out, name)
        img.resize((size, size), Image.LANCZOS).save(path, optimize=True)
        print(f"{name}: {size}×{size}, {os.path.getsize(path)} байт")
    svg = os.path.join(out, "icon.svg")
    with open(svg, "w", encoding="utf-8") as f:
        f.write(SVG)
    print(f"icon.svg: {os.path.getsize(svg)} байт")


if __name__ == "__main__":
    main()
