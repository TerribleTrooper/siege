#!/usr/bin/env python3
"""Саундтреки «Осады»: десять бесшовных петель.

    python3 compose.py [каталог] [--only имя[,имя…]]

Всё модально и дроново: лад вместо тональных ходов, открытые квинты и
параллельный органум вместо аккордов, нечётные размеры. Фоновые треки нарочно
беднее меню — в них нет мелодии, за которую цепляется слух.
"""

import json
import os
import sys

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from synth import (SR, Mix, bell, bowed, choir, deg, flute, hz, lfo, lp, noise,
                   pluck, rnd, seed, tambour, wind, write_wav, xfade_env)


def organum(mode, root, g, par):
    """Параллельный голос, но не тритоном: где квинта лада оказывается
    уменьшенной, певцы брали кварту. Возвращает сдвиг в ступенях."""
    base = deg(mode, root, g)
    for cand in (par, par + (1 if par > 0 else -1)):
        if abs(deg(mode, root, g + cand) - base) % 12 in (5, 7):
            return cand
    return par


def line(mix, motif, t0, beat, mode, root, inst, amp, pan=0.0, wet=0.45,
         hold=0.95, par=None, par_amp=0.68, par_pan=None, **kw):
    """Проигрывает мотив [(доля, длительность в долях, ступень)].

    par — параллельный голос в ступенях: -4 квинта вниз, +3 кварта вверх.
    В ладу такой голос местами даёт тритон вместо квинты — это и есть
    средневековый органум, а не ошибка."""
    for b, d, g in motif:
        dur = d * beat * hold
        mix.add(inst(hz(deg(mode, root, g)), dur, amp=amp, **kw), t0 + b * beat, pan=pan, wet=wet)
        if par is not None:
            mix.add(inst(hz(deg(mode, root, g + organum(mode, root, g, par))), dur,
                         amp=amp * par_amp, **kw),
                    t0 + b * beat, pan=par_pan if par_pan is not None else -pan, wet=wet)


def drone(mix, mode, root, degrees, amp, inst=bowed, xf=2.6, pan=0.0, wet=0.3, **kw):
    """Непрерывный дрон на всю петлю: длиннее петли на xf, хвост ложится
    на начало и склеивает шов."""
    for i, g in enumerate(degrees):
        d = mix.dur + xf
        p = pan if i % 2 == 0 else -pan
        mix.add(inst(hz(deg(mode, root, g)), d, amp=amp, xf=xf, **kw), 0.0, pan=p, wet=wet)


# ══ меню ══════════════════════════════════════════════════════════════════════

def t01_stiag():
    """Чёрный стяг — фригийский, органум на виелах, 7/4, глухой барабан."""
    seed(101)
    mode, root, beat, bpb, nb = "phrygian", 50, 60 / 54, 7, 11
    m = Mix(nb * bpb * beat, rev=3.8, damp=3400, s=17)
    drone(m, mode, root, [-7, -3], 0.2, rough=0.1, bright=0.34, wet=0.26, pan=0.25)
    drone(m, mode, root, [0], 0.075, inst=choir, wet=0.5, dark=0.75)

    A = [(0, 2, 7), (2, 1, 8), (3, 2, 7), (5, 2, 5)]
    B = [(0, 3, 8), (3, 2, 9), (5, 2, 8)]
    C = [(0, 2, 10), (2, 2, 9), (4, 1, 8), (5, 2, 7)]
    D = [(0, 4, 5), (4, 3, 6)]
    E = [(0, 7, 7)]
    plan = [A, B, A, C, D, E, A, B, C, D, E]
    for b, mot in enumerate(plan):
        t0 = b * bpb * beat
        quiet = mot is E
        line(m, mot, t0, beat, mode, root, bowed, 0.115 if not quiet else 0.085,
             pan=-0.2, wet=0.52, par=-4, par_amp=0.6, atk=0.3, rel=0.7, bright=0.42,
             vib=(4.1, 0.0035))
        if 2 <= b <= 9 and not quiet:
            for bt, a in ((0, 1.0), (2, 0.45), (4, 0.62), (5.5, 0.3)):
                m.add(tambour(0.7, amp=0.062 * a, tone=84, bright=0.4), t0 + bt * beat,
                      pan=0.1, wet=0.35)
        if b % 2 == 1:
            for i, g in enumerate((0, 4, 7, 11, 7)):
                m.add(pluck(hz(deg(mode, root, g)), 2.6, amp=0.055, bright=0.8),
                      t0 + (0.5 + i * 1.3) * beat, pan=0.42, wet=0.6)
    return m.render(tone=8600, drive=1.15, rms_db=-19.0)


def t02_vorota():
    """Каменные ворота — эолийский, хор параллельными квартами, арфа, колокол."""
    seed(202)
    mode, root, beat, bpb, nb = "aeolian", 45, 60 / 48, 4, 16
    m = Mix(nb * bpb * beat, rev=4.4, damp=3100, s=23)
    drone(m, mode, root, [-7], 0.18, bright=0.26, wet=0.22)
    drone(m, mode, root, [-3, 0], 0.06, inst=choir, wet=0.55, dark=0.7, pan=0.3)

    A = [(0, 2, 7), (2, 2, 9)]
    B = [(0, 3, 10), (3, 1, 9)]
    C = [(0, 2, 8), (2, 2, 7)]
    D = [(0, 4, 5)]
    E = [(0, 2, 11), (2, 2, 10)]
    F = [(0, 4, 7)]
    plan = [A, C, B, D, A, E, B, F, C, A, E, D, B, C, A, F]
    for b, mot in enumerate(plan):
        t0 = b * bpb * beat
        line(m, mot, t0, beat, mode, root, choir, 0.085, pan=-0.22, wet=0.62,
             par=3, par_amp=0.72, atk=1.1, rel=1.8, dark=0.55, hold=1.15)
        arp = (0, 4, 7, 11, 14, 11, 7, 4)
        if b % 2 == 0:
            for i, g in enumerate(arp):
                m.add(pluck(hz(deg(mode, root, g)), 3.4, amp=0.05 * (1.0 - 0.04 * i),
                            bright=0.95, inharm=0.0002),
                      t0 + i * 0.5 * beat, pan=0.38, wet=0.66)
        if b % 4 == 0:
            m.add(bell(hz(deg(mode, root, 0 if b % 8 == 0 else 4)), 7.0,
                       amp=0.075, dark=0.62), t0, pan=-0.45, wet=0.8)
    return m.render(tone=8200, drive=1.1, rms_db=-19.0)


def t03_rassvet():
    """Рассвет перед приступом — дорийский, 6/8, флейта с лютней и бубном."""
    seed(303)
    mode, root, beat, bpb, nb = "dorian", 43, 60 / 100, 6, 24
    m = Mix(nb * bpb * beat, rev=3.2, damp=4200, s=31)
    drone(m, mode, root, [0, 4], 0.14, bright=0.4, wet=0.24, pan=0.2)

    A = [(0, 2, 11), (2, 1, 12), (3, 2, 11), (5, 1, 9)]
    B = [(0, 1, 11), (1, 1, 12), (2, 2, 14), (4, 2, 12)]
    C = [(0, 3, 9), (3, 3, 10)]
    D = [(0, 2, 7), (2, 2, 9), (4, 2, 11)]
    R = []
    plan = [A, C, A, B, D, C, A, B, R, A, C, D, B, A, C, R,
            A, B, D, C, A, B, C, R]
    for b, mot in enumerate(plan):
        t0 = b * bpb * beat
        line(m, mot, t0, beat, mode, root, flute, 0.105, pan=-0.18, wet=0.5,
             atk=0.09, rel=0.28, breath=0.34, hold=0.9)
        lute = ((0, 0), (1.5, 4), (3, 7), (4.5, 4)) if b % 2 == 0 else ((0, -3), (2, 4), (4, 7))
        for bt, g in lute:
            m.add(pluck(hz(deg(mode, root, g)), 2.2, amp=0.062, bright=0.7),
                  t0 + bt * beat, pan=0.34, wet=0.45)
        if b >= 4 and plan[b] is not R:
            for bt, a in ((0, 1.0), (2, 0.4), (3, 0.72), (5, 0.36)):
                m.add(tambour(0.4, amp=0.05 * a, tone=112, bright=0.6, slap=0.8),
                      t0 + bt * beat, pan=0.12, wet=0.3)
    return m.render(tone=9200, drive=1.1, rms_db=-19.5)


def t04_korol():
    """Король во дворе — миксолидийский, свободный ритм, одинокая дудка."""
    seed(404)
    mode, root = "mixolydian", 48
    m = Mix(88.0, rev=4.8, damp=2900, s=41)
    drone(m, mode, root, [-7, -3], 0.17, bright=0.3, wet=0.24, pan=0.22)
    drone(m, mode, root, [0], 0.05, inst=choir, wet=0.6, dark=0.72)

    # свободная фраза: время в секундах, не по сетке
    phrase = [(0.0, 3.2, 7), (3.6, 1.4, 9), (5.2, 2.6, 8), (8.4, 4.2, 7),
              (14.0, 2.0, 10), (16.3, 1.2, 9), (17.8, 3.4, 7), (22.0, 5.0, 4),
              (29.0, 2.4, 7), (31.8, 1.1, 8), (33.2, 1.1, 9), (34.6, 3.8, 11),
              (39.5, 4.6, 10), (46.0, 2.2, 7), (48.6, 1.3, 6), (50.2, 4.0, 4),
              (56.0, 3.0, 9), (59.5, 1.2, 10), (61.0, 2.8, 9), (64.4, 4.4, 7),
              (70.5, 2.0, 5), (73.0, 1.2, 6), (74.5, 3.0, 7), (78.4, 6.0, 0)]
    for t, d, g in phrase:
        m.add(flute(hz(deg(mode, root, g)), d, amp=0.1, atk=0.16, rel=0.5,
                    breath=0.3, vib=(4.4, 0.0055)), t, pan=-0.16, wet=0.58)
    for t, g in ((1.0, 0), (6.0, 4), (12.5, 0), (20.0, -3), (27.0, 0), (33.0, 4),
                 (44.0, 0), (54.0, -3), (62.0, 0), (72.0, 4), (82.0, 0)):
        m.add(pluck(hz(deg(mode, root, g)), 4.5, amp=0.055, bright=0.62, ring=2.6),
              t, pan=0.36, wet=0.55)
    return m.render(tone=8400, drive=1.08, rms_db=-19.5)


def t05_trehryadie():
    """Трёхрядье — эолийский, колёсная лира, цимбальное остинато, 5/4."""
    seed(505)
    mode, root, beat, bpb, nb = "aeolian", 40, 60 / 60, 5, 18
    m = Mix(nb * bpb * beat, rev=3.6, damp=3600, s=53)
    drone(m, mode, root, [0, 4], 0.21, rough=0.16, bright=0.38, wet=0.26, pan=0.28)
    drone(m, mode, root, [7], 0.055, inst=choir, wet=0.6, dark=0.68)

    cell = ((0, 7), (1, 11), (2, 10), (3, 7), (4, 9))
    A = [(0, 2, 14), (2, 1, 13), (3, 2, 11)]
    B = [(0, 3, 12), (3, 2, 11)]
    C = [(0, 1, 11), (1, 1, 12), (2, 3, 14)]
    D = [(0, 5, 9)]
    plan = [None, A, None, B, A, C, None, D, A, B, None, C, A, D, None, B, C, None]
    for b, mot in enumerate(plan):
        t0 = b * bpb * beat
        for bt, g in cell:
            amp = 0.058 if b % 2 == 0 else 0.046
            m.add(pluck(hz(deg(mode, root, g)), 2.0, amp=amp, bright=1.0, inharm=0.0003),
                  t0 + bt * beat + (0.03 if bt % 2 else 0.0), pan=0.4 - 0.16 * (bt % 2),
                  wet=0.52)
        if mot:
            line(m, mot, t0, beat, mode, root, bowed, 0.1, pan=-0.24, wet=0.56,
                 par=-4, par_amp=0.55, atk=0.4, rel=0.9, bright=0.44)
    return m.render(tone=8800, drive=1.12, rms_db=-19.0)


# ══ игра ══════════════════════════════════════════════════════════════════════

def t06_rov():
    """Ров с водой — фоновая постель без пульса: дрон, вздохи хора, редкий щипок."""
    seed(606)
    mode, root = "aeolian", 38
    m = Mix(96.0, rev=5.2, damp=2600, s=61)
    drone(m, mode, root, [0, 4], 0.2, bright=0.22, wet=0.2, pan=0.2, vib=(3.2, 0.0025))
    n = int(SR * (96.0 + 2.6))
    for g, per, ph in ((7, 32.0, 0.0), (11, 48.0, 2.1), (10, 24.0, 4.2)):
        a = 0.35 + 0.65 * lfo(n, per, 1.0, ph)
        m.add(choir(hz(deg(mode, root, g)), 98.6, amp=0.05, amp_env=a * xfade_env(n, 2.6),
                    dark=0.8), 0.0, pan=0.3 if g % 2 else -0.3, wet=0.62)
    m.add(wind(98.6, amp=0.03, lo=220, hi=1100, period=17.0), 0.0, pan=0.0, wet=0.4)
    for t, g in ((6.0, 7), (19.5, 11), (34.0, 10), (47.0, 7), (58.5, 4),
                 (71.0, 11), (84.0, 7)):
        m.add(pluck(hz(deg(mode, root, g)), 5.0, amp=0.042, bright=0.55, ring=2.8),
              t, pan=rnd(-0.45, 0.45), wet=0.75)
    return m.render(tone=7200, drive=1.05, rms_db=-21.5)


def t07_chasovoy():
    """Тихий часовой — фригийский, 7/8, редкая лютня с длинными паузами."""
    seed(707)
    mode, root, beat, bpb, nb = "phrygian", 45, 0.55, 7, 24
    m = Mix(nb * bpb * beat, rev=4.6, damp=2800, s=71)
    drone(m, mode, root, [-7, 0], 0.16, bright=0.2, wet=0.22, pan=0.18)

    figs = [((0, 7), (2, 8), (5, 7)),
            ((0, 5), (3, 4)),
            ((1, 9), (3, 8), (4, 7)),
            ((0, 7),),
            ((2, 11), (4, 10)),
            ((0, 4), (2, 5), (4, 4), (6, 2))]
    plan = [0, None, 1, None, None, 2, None, 3, None, 4, None, 1,
            None, None, 5, None, 0, None, 2, None, None, 3, None, None]
    for b, p in enumerate(plan):
        if p is None:
            continue
        t0 = b * bpb * beat
        for bt, g in figs[p]:
            m.add(pluck(hz(deg(mode, root, g)), 3.0, amp=0.05, bright=0.6, ring=2.2),
                  t0 + bt * beat, pan=rnd(-0.3, 0.3), wet=0.68)
    for t in (12.0, 38.0, 64.0):
        m.add(choir(hz(deg(mode, root, 4)), 14.0, amp=0.035, atk=4.0, rel=7.0, dark=0.82),
              t, pan=-0.25, wet=0.6)
    return m.render(tone=7000, drive=1.04, rms_db=-22.0)


def t08_rudnik():
    """Серебро рудника — дорийский, 5/4, цимбальная ячейка и дышащий пад."""
    seed(808)
    mode, root, beat, bpb, nb = "dorian", 41, 60 / 66, 5, 20
    m = Mix(nb * bpb * beat, rev=4.0, damp=3000, s=83)
    drone(m, mode, root, [0], 0.15, bright=0.24, wet=0.22)
    n = int(SR * (m.dur + 2.6))
    m.add(choir(hz(deg(mode, root, 4)), m.dur + 2.6, amp=0.045,
                amp_env=(0.4 + 0.6 * lfo(n, m.dur / 3.0)) * xfade_env(n, 2.6), dark=0.78),
          0.0, pan=0.28, wet=0.6)

    cell = ((0, 7), (1.5, 9), (3, 7), (4, 4))
    alt = ((0, 7), (2, 10), (3.5, 9))
    for b in range(nb):
        t0 = b * bpb * beat
        c = cell if b % 4 != 3 else alt
        for bt, g in c:
            m.add(pluck(hz(deg(mode, root, g)), 2.4, amp=0.046 if b % 2 else 0.054,
                        bright=0.85, inharm=0.0003),
                  t0 + bt * beat, pan=0.34 - 0.14 * (bt % 2), wet=0.58)
        if b % 5 == 2:
            m.add(pluck(hz(deg(mode, root, 0)), 5.0, amp=0.05, bright=0.4, ring=3.0),
                  t0, pan=-0.3, wet=0.5)
    return m.render(tone=7600, drive=1.06, rms_db=-21.5)


def t09_ozhidanie():
    """Долгое ожидание — почти тишина: низкий дрон, ветер, далёкий колокол."""
    seed(909)
    mode, root = "aeolian", 36
    m = Mix(88.0, rev=5.6, damp=2400, s=97)
    drone(m, mode, root, [0, 4], 0.19, bright=0.16, wet=0.18, pan=0.16, vib=(2.8, 0.002))
    m.add(wind(90.6, amp=0.042, lo=140, hi=760, period=13.0), 0.0, pan=-0.2, wet=0.45)
    m.add(wind(90.6, amp=0.03, lo=600, hi=2200, period=19.0), 0.0, pan=0.3, wet=0.55)
    n = int(SR * 90.6)
    m.add(choir(hz(deg(mode, root, 7)), 90.6, amp=0.038,
                amp_env=(0.3 + 0.7 * lfo(n, 44.0, 1.0, 1.7)) * xfade_env(n, 2.6), dark=0.85),
          0.0, pan=0.24, wet=0.66)
    for t, g in ((4.0, 0), (26.0, 4), (48.0, 0), (69.0, 3)):
        m.add(bell(hz(deg(mode, root, g + 14)), 9.0, amp=0.052, dark=0.7), t,
              pan=rnd(-0.5, 0.5), wet=0.85)
    return m.render(tone=6400, drive=1.03, rms_db=-22.5)


def t10_po_stenam():
    """Ветром по стенам — дорийский, статичный воздух, флажолеты смычка."""
    seed(1010)
    mode, root = "dorian", 45
    m = Mix(94.0, rev=5.0, damp=3200, s=107)
    drone(m, mode, root, [-7, -3], 0.17, bright=0.2, wet=0.2, pan=0.22)
    n = int(SR * 96.6)
    for g, per, ph, pan in ((11, 31.0, 0.0, -0.32), (14, 47.0, 1.9, 0.34),
                            (18, 23.5, 3.6, 0.0)):
        a = (0.25 + 0.75 * lfo(n, per, 1.0, ph)) * xfade_env(n, 2.6)
        m.add(bowed(hz(deg(mode, root, g)), 96.6, amp=0.036, amp_env=a, bright=0.3,
                    vib=(3.6, 0.003)), 0.0, pan=pan, wet=0.7)
    m.add(wind(96.6, amp=0.028, lo=400, hi=1800, period=21.0), 0.0, pan=0.0, wet=0.5)
    for t, g in ((9.0, 11), (28.0, 14), (44.0, 10), (61.0, 14), (79.0, 11)):
        m.add(pluck(hz(deg(mode, root, g)), 4.2, amp=0.03, bright=0.9, ring=2.4),
              t, pan=rnd(-0.4, 0.4), wet=0.8)
    return m.render(tone=7800, drive=1.03, rms_db=-22.5)


TRACKS = [
    ("menu-1-cherniy-stiag",  "Чёрный стяг",              "меню", t01_stiag),
    ("menu-2-kamennye-vorota", "Каменные ворота",         "меню", t02_vorota),
    ("menu-3-rassvet",        "Рассвет перед приступом",  "меню", t03_rassvet),
    ("menu-4-korol-vo-dvore", "Король во дворе",          "меню", t04_korol),
    ("menu-5-trehryadie",     "Трёхрядье",                "меню", t05_trehryadie),
    ("game-1-rov-s-vodoy",    "Ров с водой",              "игра", t06_rov),
    ("game-2-tihiy-chasovoy", "Тихий часовой",            "игра", t07_chasovoy),
    ("game-3-serebro-rudnika", "Серебро рудника",         "игра", t08_rudnik),
    ("game-4-dolgoe-ozhidanie", "Долгое ожидание",        "игра", t09_ozhidanie),
    ("game-5-vetrom-po-stenam", "Ветром по стенам",       "игра", t10_po_stenam),
]


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    out = args[0] if args else "out"
    only = []
    if "--only" in sys.argv:
        only = [x for x in sys.argv[sys.argv.index("--only") + 1].split(",") if x]
    os.makedirs(out, exist_ok=True)
    meta = []
    for slug, title, kind, fn in TRACKS:
        if only and not any(o in slug for o in only):
            continue
        x = fn()
        path = os.path.join(out, slug + ".wav")
        write_wav(path, x)
        loop = x.shape[1] / SR
        peak = 20 * np.log10(float(np.max(np.abs(x))) + 1e-12)
        rms = 20 * np.log10(float(np.sqrt(np.mean(x ** 2))) + 1e-12)
        meta.append({"slug": slug, "title": title, "kind": kind,
                     "loop": round(loop, 3), "peak_db": round(peak, 1),
                     "rms_db": round(rms, 1)})
        print(f"{slug:26s} {title:24s} {loop:6.2f}s  peak {peak:5.1f}  rms {rms:5.1f}")
    with open(os.path.join(out, "tracks.json"), "w", encoding="utf-8") as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)


if __name__ == "__main__":
    main()
