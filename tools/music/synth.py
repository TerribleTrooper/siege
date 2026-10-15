"""Синтез инструментов и микс для саундтреков «Осады».

Ничего внешнего не нужно: numpy + scipy, на выходе WAV. Всё детерминировано —
один и тот же seed даёт один и тот же трек, поэтому треки не хранятся в git
как исходники, а пересобираются.
"""

import numpy as np
import wave
from scipy.signal import butter, sosfilt, fftconvolve

SR = 44100
_rng = np.random.default_rng(1)


def seed(s):
    global _rng
    _rng = np.random.default_rng(s)


def noise(n):
    return _rng.standard_normal(n)


def rnd(a=0.0, b=1.0):
    return float(_rng.uniform(a, b))


# ── фильтры ───────────────────────────────────────────────────────────────────

def lp(x, fc, order=2):
    fc = min(max(fc, 20.0), SR * 0.47)
    return sosfilt(butter(order, fc / (SR / 2), btype="low", output="sos"), x)


def hp(x, fc, order=2):
    fc = min(max(fc, 10.0), SR * 0.47)
    return sosfilt(butter(order, fc / (SR / 2), btype="high", output="sos"), x)


def bp(x, f1, f2, order=2):
    f1 = max(f1, 20.0)
    f2 = min(f2, SR * 0.47)
    if f2 <= f1:
        f2 = f1 * 1.2
    return sosfilt(butter(order, [f1 / (SR / 2), f2 / (SR / 2)], btype="band", output="sos"), x)


# ── огибающие ─────────────────────────────────────────────────────────────────

def env(n, atk, rel, curve=2.0):
    """Атака линейно, спад по степенной кривой. Сжимается, если нота короче."""
    e = np.ones(n)
    na = max(1, int(atk * SR))
    nr = max(1, int(rel * SR))
    if na + nr > n:
        k = n / (na + nr)
        na = max(1, int(na * k))
        nr = max(1, n - na)
    e[:na] = np.linspace(0.0, 1.0, na)
    e[n - nr:] = np.linspace(1.0, 0.0, nr) ** curve
    return e


def xfade_env(n, xf):
    """Огибающая для бесшовного дрона: вход и выход зеркальны, их складывает
    свёртка петли (хвост кладётся на начало)."""
    e = np.ones(n)
    k = max(1, int(xf * SR))
    k = min(k, n // 2)
    e[:k] = np.linspace(0.0, 1.0, k)
    e[n - k:] = np.linspace(1.0, 0.0, k)
    return e


def lfo(n, period, depth=1.0, phase=0.0, base=0.0):
    t = np.arange(n) / SR
    return base + depth * 0.5 * (1.0 + np.sin(2 * np.pi * t / period + phase))


# ── строй ─────────────────────────────────────────────────────────────────────

MODES = {
    "dorian":     [0, 2, 3, 5, 7, 9, 10],
    "phrygian":   [0, 1, 3, 5, 7, 8, 10],
    "aeolian":    [0, 2, 3, 5, 7, 8, 10],
    "mixolydian": [0, 2, 4, 5, 7, 9, 10],
}


def deg(mode, root, d):
    """Ступень лада в midi. d целое, может быть отрицательным и больше 7."""
    o, i = divmod(int(d), 7)
    return root + 12 * o + MODES[mode][i]


def hz(m):
    return 440.0 * 2.0 ** ((m - 69) / 12.0)


# ── инструменты ───────────────────────────────────────────────────────────────

def _norm(x, amp):
    m = float(np.max(np.abs(x)))
    return amp * x / (m + 1e-12)


def pluck(f, dur, amp=0.25, bright=1.0, ring=None, inharm=0.0004, body=6500):
    """Щипок: псалтерий / лютня / арфа. Аддитивно, затухание выше — быстрее."""
    dur = float(min(dur, 7.0))
    n = max(64, int(SR * dur))
    t = np.arange(n) / SR
    base = ring if ring else min(3.2, 0.9 + 90.0 / f)
    ph = rnd(0, 6.283)
    out = np.zeros(n)
    for k in range(1, 17):
        fk = f * k * (1.0 + inharm * k * k)
        if fk > 15000:
            break
        a = (1.0 / k ** 1.35) * (0.62 + 0.38 * np.cos(k * 0.8 + ph))
        a *= bright ** (k * 0.1)
        out += a * np.exp(-t / (base / k ** 0.72)) * np.sin(2 * np.pi * fk * t + ph * k * 0.37)
    out *= env(n, 0.004, min(0.15, dur * 0.25))
    return _norm(lp(out, body), amp)


def bowed(f, dur, amp=0.18, atk=0.35, rel=0.6, bright=0.5, vib=(4.3, 0.004),
          rough=0.0, amp_env=None, xf=None):
    """Смычок: виела, колёсная лира, низкий органный голос."""
    n = max(64, int(SR * float(dur)))
    t = np.arange(n) / SR
    fv, dv = vib
    inst = f * (1.0 + dv * np.sin(2 * np.pi * fv * t + rnd(0, 6.283)))
    ph = 2 * np.pi * np.cumsum(inst) / SR + rnd(0, 6.283)
    out = np.zeros(n)
    for k in range(1, 19):
        if f * k > 12500:
            break
        out += (1.0 / k ** (1.55 - 0.55 * bright)) * np.sin(k * ph)
    out = lp(out, 1800 + 3200 * bright)
    if rough:
        out = out * (1.0 + rough * lp(noise(n), 11) * 1.5)
    if xf is not None:
        out *= xfade_env(n, xf)
    elif amp_env is not None:
        out *= amp_env
    else:
        out *= env(n, atk, rel, curve=1.6)
    return _norm(out, amp)


def flute(f, dur, amp=0.16, atk=0.14, rel=0.3, breath=0.3, vib=(4.8, 0.005)):
    """Блокфлейта / продольная дудка: почти синус плюс шум дыхания."""
    n = max(64, int(SR * float(dur)))
    t = np.arange(n) / SR
    fv, dv = vib
    inst = f * (1.0 + dv * np.sin(2 * np.pi * fv * t + rnd(0, 6.283)) * np.minimum(1.0, t / 0.4))
    ph = 2 * np.pi * np.cumsum(inst) / SR
    out = np.sin(ph) + 0.16 * np.sin(2 * ph + 1.1) + 0.05 * np.sin(3 * ph)
    air = bp(noise(n), f * 1.4, f * 4.0) * breath * 0.5
    out = out + air
    out *= env(n, atk, rel, curve=1.4)
    return _norm(lp(out, 6000), amp)


def choir(f, dur, amp=0.12, atk=0.9, rel=1.4, amp_env=None, xf=None, dark=0.5):
    """Голосовой пад: расстроенные пилы через формантные полосы."""
    n = max(64, int(SR * float(dur)))
    t = np.arange(n) / SR
    out = np.zeros(n)
    for det in (-0.5, 0.0, 0.42, 0.9):
        fd = f * 2.0 ** (det / 1200.0 * 7.0)
        drift = 1.0 + 0.0018 * np.sin(2 * np.pi * (0.07 + rnd(0, 0.05)) * t + rnd(0, 6.283))
        ph = 2 * np.pi * np.cumsum(fd * drift) / SR + rnd(0, 6.283)
        for k in range(1, 13):
            if fd * k > 11000:
                break
            out += (1.0 / k ** 1.3) * np.sin(k * ph)
    form = 0.9 * bp(out, 300, 900) + 0.6 * bp(out, 1000, 2000) + 0.35 * lp(out, 260)
    form = lp(form, 3000 + 2500 * (1.0 - dark))
    if xf is not None:
        form *= xfade_env(n, xf)
    elif amp_env is not None:
        form *= amp_env
    else:
        form *= env(n, atk, rel, curve=1.5)
    return _norm(form, amp)


def bell(f, dur, amp=0.12, dark=0.6):
    """Далёкий колокол: неармоничные партиалы."""
    n = max(64, int(SR * float(dur)))
    t = np.arange(n) / SR
    out = np.zeros(n)
    for r, a, d in ((1.0, 1.0, 1.0), (2.0, 0.5, 0.62), (2.76, 0.42, 0.45),
                    (4.07, 0.22, 0.3), (5.43, 0.13, 0.2), (6.8, 0.08, 0.13)):
        if f * r > 14000:
            break
        out += a * np.exp(-t / (dur * d * 0.42)) * np.sin(2 * np.pi * f * r * t + rnd(0, 6.283))
    strike = lp(noise(n), 2600) * np.exp(-t / 0.012) * 0.25
    out = lp(out + strike, 4200 - 1800 * dark)
    out *= env(n, 0.002, min(0.4, dur * 0.3))
    return _norm(out, amp)


def tambour(dur=0.55, amp=0.1, tone=92.0, bright=0.45, slap=0.6):
    """Рамочный барабан: глухой тон плюс короткий шлепок кожи."""
    n = max(64, int(SR * float(dur)))
    t = np.arange(n) / SR
    skin = lp(noise(n), 500 + 1800 * bright) * np.exp(-t / (0.035 + 0.05 * slap))
    body = (np.sin(2 * np.pi * tone * t) + 0.4 * np.sin(2 * np.pi * tone * 1.6 * t)) * np.exp(-t / 0.1)
    out = 0.75 * slap * skin + body
    out *= env(n, 0.001, min(0.2, dur * 0.4))
    return _norm(lp(out, 5200), amp)


def wind(dur, amp=0.05, lo=180.0, hi=900.0, period=11.0, xf=2.5):
    """Ветровой слой: узкополосный шум с медленным дыханием."""
    n = max(64, int(SR * float(dur)))
    x = bp(noise(n), lo, hi, order=2)
    x *= (0.45 + 0.55 * lfo(n, period, 1.0)) * (0.6 + 0.4 * lfo(n, period * 2.7, 1.0, 1.3))
    x *= xfade_env(n, xf)
    return _norm(x, amp)


# ── реверберация и микс ───────────────────────────────────────────────────────

def make_ir(dur=3.4, predelay=0.025, damp=3800.0, s=11):
    r = np.random.default_rng(s)
    n = int(SR * dur)
    t = np.arange(n) / SR
    ir = r.standard_normal(n) * np.exp(-t * (6.5 / dur))
    ir = lp(ir, damp)
    for d, g in ((0.011, 0.5), (0.023, -0.38), (0.037, 0.3), (0.059, -0.22), (0.083, 0.16)):
        i = int(d * SR)
        ir[i:i + 400] += g * np.hanning(400) * 0.6
    p = int(predelay * SR)
    ir[:p] = 0.0
    ir = hp(ir, 110)
    return ir / (np.sqrt(np.sum(ir ** 2)) + 1e-12)


class Mix:
    """Петля длиной dur: хвосты уезжают в запас и складываются на начало."""

    PAD = 10.0

    def __init__(self, dur, rev=3.4, damp=3800.0, s=11):
        self.dur = float(dur)
        self.n = int(SR * self.dur)
        self.pad = int(SR * self.PAD)
        self.m = self.n + self.pad
        self.l = np.zeros(self.m)
        self.r = np.zeros(self.m)
        self.send = np.zeros(self.m)
        self.irl = make_ir(rev, 0.025, damp, s)
        self.irr = make_ir(rev * 1.06, 0.031, damp * 0.92, s + 101)

    def add(self, x, t, pan=0.0, wet=0.45, gain=1.0):
        i = int(float(t) * SR)
        if i < 0 or i >= self.m:
            return
        k = min(len(x), self.m - i)
        seg = x[:k] * gain
        a = (pan + 1.0) * np.pi / 4.0
        sl, sr = np.cos(a), np.sin(a)
        self.l[i:i + k] += seg * sl
        self.r[i:i + k] += seg * sr
        self.send[i:i + k] += seg * wet

    @staticmethod
    def _circ(fn, x, pre=SR):
        """Фильтрует петлю по кругу: перед началом подставляет её же хвост,
        иначе фильтр запускается с нуля и на шве остаётся щелчок."""
        ext = np.concatenate([x[:, -pre:], x], axis=1)
        return fn(ext)[:, pre:]

    def render(self, tone=9000.0, low=34.0, drive=1.1, rms_db=-19.0, peak=0.9):
        wl = fftconvolve(self.send, self.irl)[:self.m]
        wr = fftconvolve(self.send, self.irr)[:self.m]
        l = self.l + wl
        r = self.r + wr
        # петля: хвост на начало
        l[:self.pad] += l[self.n:self.n + self.pad]
        r[:self.pad] += r[self.n:self.n + self.pad]
        l, r = l[:self.n], r[:self.n]
        out = np.vstack([l, r])
        out = self._circ(lambda y: hp(y, low, order=2), out)
        out = self._circ(lambda y: lp(y, tone, order=2), out)
        out = np.tanh(out * drive) / np.tanh(drive)
        rms = float(np.sqrt(np.mean(out ** 2)))
        g = (10.0 ** (rms_db / 20.0)) / (rms + 1e-12)
        if float(np.max(np.abs(out))) * g > peak:
            g = peak / (float(np.max(np.abs(out))) + 1e-12)
        return out * g


def write_wav(path, stereo):
    x = np.clip(stereo, -1.0, 1.0)
    pcm = (x.T * 32767.0).astype("<i2")
    with wave.open(path, "wb") as w:
        w.setnchannels(2)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(pcm.tobytes())
