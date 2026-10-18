#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Варианты второй карты «Осады»: замок в центре, осаждающие в противоположных
углах, река сквозь замок, форпост у истока.

Почему карта квадратная 44x44 (1936 клеток против 616 — ровно 3.1x):
при лагерях в противоположных углах (0,0) и (N-1,N-1) множество точек,
равноудалённых от обоих по Чебышёву — это ровно антидиагональ x+y=N-1.
Значит река с истоком и форпост обязаны лежать на ней, иначе один из
осаждающих ближе к запруде. Отражение относительно той же антидиагонали
(x,y)->(N-1-y,N-1-x) меняет лагеря местами и оставляет реку на месте,
поэтому карта честна по построению, а не на глаз.
"""
import json, collections, math, os

N = 44
LAST = N - 1
AXIS = LAST

# . поле  B кусты  V обрыв  R скалы  M рудник  F пашня  D ров  C двор
# W боевой ход  # стена  T башня  G ворота  a/b лагеря
# новое: ~ река   = мост/гать-настил   g водяные ворота (арка)
#        O форпост   ^ подъём на плато
FIELD = set('.BVRMF')
PASSABLE = set('.BMF=O^ab')
CASTLE = set('DCW#TGg')


def mir(x, y):
    """Зеркало относительно антидиагонали: меняет лагеря местами."""
    return LAST - y, LAST - x


def rot180(x, y):
    """Поворот на пол-оборота вокруг замка: тоже меняет лагеря местами.

    В координатах вдоль оси u=(x+y-43)/2 (поперёк) и v=(x-y)/2 (вдоль)
    зеркало даёт (u,v)->(-u,v) и потому требует u(v)=-u(v), то есть строго
    прямого русла: любая излучина зеркало ломает. Поворот даёт (u,v)->(-u,-v)
    и излучину разрешает — при условии, что она нечётна относительно центра:
    каждый изгиб к северо-западу отвечает такому же изгибу к юго-востоку."""
    return LAST - x, LAST - y


def noise(x, y, salt=0):
    h = (x * 73856093) ^ (y * 19349663) ^ (salt * 83492791)
    h = (h ^ (h >> 13)) * 1274126177
    return ((h ^ (h >> 16)) & 0xffff) / 0xffff


class Map:
    def __init__(self, key, name, tag, note, mechanics, tradeoff, sym='mirror'):
        self.key, self.name, self.tag = key, name, tag
        self.note, self.mechanics, self.tradeoff = note, mechanics, tradeoff
        self.sym = sym
        self.twin = mir if sym == 'mirror' else rot180
        self.g = [['.'] * N for _ in range(N)]
        self.marks = []
        self.crossings = {'court': 0, 'field': 0}
        self.river, self.deck_cells, self.fort_cells = set(), set(), set()

    def put(self, x, y, ch):
        """Ставит клетку и её зеркало — половину карты описывать не надо,
        а нарушить симметрию случайно нельзя."""
        if 0 <= x < N and 0 <= y < N:
            self.g[y][x] = ch
            mx, my = self.twin(x, y)
            self.g[my][mx] = ch

    def at(self, x, y):
        return self.g[y][x] if 0 <= x < N and 0 <= y < N else None

    def ring(self, lo, hi, ch, over=None):
        for i in range(lo, hi + 1):
            for p in ((i, lo), (i, hi), (lo, i), (hi, i)):
                if over is None or self.at(*p) in over:
                    self.put(*p, ch)

    def fill(self, lo, hi, ch, over=None):
        for y in range(lo, hi + 1):
            for x in range(lo, hi + 1):
                if over is None or self.at(x, y) in over:
                    self.put(x, y, ch)

    def mark(self, x, y, text):
        self.marks.append({'x': x, 'y': y, 'text': text})


def blob(m, cx, cy, r, ch, density=0.8, over=FIELD):
    """Пятно рельефа вместе с зеркалом: к краю редеет."""
    for dy in range(-r, r + 1):
        for dx in range(-r, r + 1):
            d = max(abs(dx), abs(dy))
            if d > r:
                continue
            fade = 1.0 - d / (r + 1.0)
            if noise(cx + dx, cy + dy, cx * 7 + cy) < density * fade:
                if m.at(cx + dx, cy + dy) in over:
                    m.put(cx + dx, cy + dy, ch)


# --------------------------------------------------------------- река

def river_cells(halfwidth=1):
    """Полоса по антидиагонали. Три слоя (halfwidth=1) — минимум, который
    нельзя перепрыгнуть по диагонали: один шаг меняет x+y не больше чем на 2."""
    return [(x, y) for y in range(N) for x in range(N)
            if abs(x + y - AXIS) <= halfwidth]


def centreline(half):
    """Полная осевая линия: половина от истока до центра плюс её поворот.
    Нечётность относительно центра — по построению, а не на глаз."""
    pts = [(float(x), float(y)) for (x, y) in half]
    return pts + [rot180(x, y) for (x, y) in reversed(pts[:-1])]


def chaikin(points, passes=2):
    """Сглаживание Чайкина: из ломаной опорных точек получается русло."""
    pts = list(points)
    for _ in range(passes):
        out = [pts[0]]
        for i in range(len(pts) - 1):
            (x0, y0), (x1, y1) = pts[i], pts[i + 1]
            out.append((x0 * .75 + x1 * .25, y0 * .75 + y1 * .25))
            out.append((x0 * .25 + x1 * .75, y0 * .25 + y1 * .75))
        out.append(pts[-1])
        pts = out
    return pts


def densify(points, step=.2):
    out = []
    for i in range(len(points) - 1):
        (x0, y0), (x1, y1) = points[i], points[i + 1]
        d = math.hypot(x1 - x0, y1 - y0)
        k = max(1, int(d / step))
        out += [(x0 + (x1 - x0) * t / k, y0 + (y1 - y0) * t / k) for t in range(k + 1)]
    out.append(points[-1])
    return out


def band_cells(samples, r=1.5):
    cells = set()
    rr = int(math.ceil(r))
    for (fx, fy) in samples:
        cx, cy = int(round(fx)), int(round(fy))
        for dx in range(-rr, rr + 1):
            for dy in range(-rr, rr + 1):
                x, y = cx + dx, cy + dy
                if 0 <= x < N and 0 <= y < N and math.hypot(x - fx, y - fy) <= r:
                    cells.add((x, y))
    return cells


def seal_diagonals(cells):
    """Затыкает диагональные лазейки: если вода идёт по диагонали, а обе
    угловые клетки — суша, отряд перешагнул бы русло одним шагом."""
    changed = True
    while changed:
        changed = False
        for (x, y) in list(cells):
            for dx, dy in ((1, 1), (-1, 1)):
                if (x + dx, y + dy) not in cells:
                    continue
                p, q = (x + dx, y), (x, y + dy)
                if p not in cells and q not in cells:
                    cells.add(p if 0 <= p[0] < N else q)
                    changed = True
    return cells


def across(samples, i, half):
    """Клетки поперёк течения: так ложится мост и так встаёт форпост."""
    j, k = min(len(samples) - 1, i + 3), max(0, i - 3)
    dx, dy = samples[j][0] - samples[k][0], samples[j][1] - samples[k][1]
    d = math.hypot(dx, dy) or 1.0
    nx, ny = -dy / d, dx / d
    fx, fy = samples[i]
    out, t = [], -half
    while t <= half:
        x, y = int(round(fx + nx * t)), int(round(fy + ny * t))
        if 0 <= x < N and 0 <= y < N and (x, y) not in out:
            out.append((x, y))
        t += .3
    return out


def nearest(samples, target):
    tx, ty = target
    return min(range(len(samples)),
               key=lambda i: math.hypot(samples[i][0] - tx, samples[i][1] - ty))


def carve_winding(m, half, r=1.5, bridges=(), post=None):
    """Проводит извилистое русло и кладёт через него переправы.

    Клетки ставятся напрямую, без put: симметрию уже обеспечила сама
    осевая линия, а зеркалить её ещё раз значило бы прорыть второе русло."""
    s = densify(chaikin(centreline(half), 2), .2)
    water = seal_diagonals(band_cells(s, r))
    for (x, y) in sorted(water):
        cur = m.at(x, y)
        if cur in ('#', 'T', 'G', 'g'):
            m.g[y][x] = 'g'          # арка: вода внизу, боевой ход наверху
        elif cur in ('W', '='):
            m.g[y][x] = '='
        else:
            m.g[y][x] = '~'
    def open_ends(line):
        """Скалы и обрывы у концов переправы убираем: мост, упирающийся в
        обрыв, мостом не является."""
        for (ex, ey) in (line[0], line[-1]):
            for dx in range(-2, 3):
                for dy in range(-2, 3):
                    x, y = ex + dx, ey + dy
                    if not (0 <= x < N and 0 <= y < N):
                        continue
                    tx, ty = m.twin(x, y)
                    # Расчищаем парой: иначе у истока карта станет просторнее,
                    # чем у устья, и симметрия рельефа поедет.
                    if m.g[y][x] in ('R', 'V') and m.g[ty][tx] in ('R', 'V'):
                        m.g[y][x] = '.'
                        m.g[ty][tx] = '.'

    m.river = set(water)
    for target in bridges:
        i = nearest(s, target)
        line = across(s, i, r + 1.4)
        for (x, y) in line:
            # Настил кладём и на клетку-близнеца: поперечник, снятый по
            # нормали, у самого центра округляется на полклетки в сторону.
            for (cx, cy) in ((x, y), m.twin(x, y)):
                if m.at(cx, cy) in ('~', 'D'):
                    m.g[cy][cx] = '='
                    m.deck_cells.add((cx, cy))
        open_ends(line)
    if post:
        i = nearest(s, post)
        line = across(s, i, r + 1.6)
        # Берём связный кусок поперечника: воду от берега до берега плюс по
        # клетке суши с каждой стороны — застава сидит на обоих берегах.
        mid = min(range(len(line)),
                  key=lambda k: math.hypot(line[k][0] - s[i][0], line[k][1] - s[i][1]))
        keep, lo, hi = [line[mid]], mid - 1, mid + 1
        while lo >= 0 and m.at(*line[lo]) in ('~', 'D'):
            keep.append(line[lo]); lo -= 1
        while hi < len(line) and m.at(*line[hi]) in ('~', 'D'):
            keep.append(line[hi]); hi += 1
        for end in (lo, hi):
            if 0 <= end < len(line) and m.at(*line[end]) in ('.', 'B', 'R'):
                keep.append(line[end])
        for (x, y) in keep:
            m.g[y][x] = 'O'
            m.fort_cells.add((x, y))
        open_ends(keep)
        m.mark(int(s[i][0]), int(s[i][1]), 'форпост · исток')
    return water


def cross_patch(off):
    """Самозеркальная переправа через диагональное русло: уголок из трёх
    клеток. Отражение сохраняет x-y, поэтому такой набор переходит в себя."""
    return [(21 + off, 21 - off), (22 + off, 21 - off), (22 + off, 22 - off)]


def carve_river(m, halfwidth=1, through_castle=True, canyon=False, bridges=()):
    for off in bridges:
        m.crossings['court' if abs(off) <= 5 else 'field'] += 1
    for (x, y) in river_cells(halfwidth):
        cur = m.at(x, y)
        if not through_castle and cur in CASTLE:
            continue                      # река упирается в озеро/обвод
        if cur in ('#', 'T', 'G', 'g'):
            m.put(x, y, 'g')              # арка: вода внизу, боевой ход наверху
        elif cur in ('W', '='):
            m.put(x, y, '=')              # мостик на боевом ходу
        else:
            m.put(x, y, '~')
    if canyon:
        for (x, y) in river_cells(halfwidth + 1):
            if m.at(x, y) in ('.', 'B'):
                m.put(x, y, 'V')          # обрывистые берега: русло в ущелье
    for off in bridges:
        patch = cross_patch(off)
        for (x, y) in patch:
            if m.at(x, y) in ('~', 'V', 'D'):
                m.put(x, y, '=')
        if canyon:                        # расчистить спуск к мосту с берегов
            for (x, y) in patch:
                for dx in (-1, 0, 1):
                    for dy in (-1, 0, 1):
                        if m.at(x + dx, y + dy) == 'V':
                            m.put(x + dx, y + dy, '.')


def outpost(m, off=17):
    """Форпост у истока стоит поперёк ручья, поэтому он же — верхняя
    переправа. Уголок самозеркален: оба лагеря равноудалены от него."""
    blob(m, LAST - 3, 3, 4, 'R', density=0.95)
    for (x, y) in river_cells(1):
        if max(abs(x - LAST), abs(y)) <= 4:
            m.put(x, y, '~')
    for (x, y) in cross_patch(off):
        m.put(x, y, 'O')
        for dx in (-1, 0, 1):
            for dy in (-1, 0, 1):
                if m.at(x + dx, y + dy) == 'R':
                    m.put(x + dx, y + dy, '.')
    m.mark(39, 4, 'форпост · исток')


def camps(m, leg=9):
    for y in range(N):
        for x in range(N):
            if x + y <= leg:
                m.g[y][x] = 'a'
            elif x + y >= 2 * LAST - leg:
                m.g[y][x] = 'b'
    m.mark(3, 3, 'лагерь A')
    m.mark(LAST - 3, LAST - 3, 'лагерь B')


def wall_ring(m, lo, hi, gates, tower_step=5):
    m.ring(lo, hi, '#')
    for i in range(lo, hi + 1):
        if (i - lo) % tower_step == 0:
            for p in ((i, lo), (i, hi), (lo, i), (hi, i)):
                m.put(*p, 'T')
    for p in ((lo, lo), (lo, hi), (hi, lo), (hi, hi)):
        m.put(*p, 'T')
    for side in gates:
        for k in (21, 22):
            if side == 'N': m.put(k, lo, 'G')
            if side == 'S': m.put(k, hi, 'G')
            if side == 'W': m.put(lo, k, 'G')
            if side == 'E': m.put(hi, k, 'G')


def gate_bridges(m, moat_lo, moat_hi, gates):
    """Мост напротив ворот: он есть и на нынешней карте — ничей, с начала
    осады, 60 прочности. Через трёхклеточное озеро — гать той же природы."""
    for side in gates:
        for k in (21, 22):
            if side == 'N':
                for y in range(moat_lo, moat_hi + 1):
                    if m.at(k, y) == 'D': m.put(k, y, '=')
                    else: break
            if side == 'W':
                for x in range(moat_lo, moat_hi + 1):
                    if m.at(x, k) == 'D': m.put(x, k, '=')
                    else: break
            if side == 'S':
                for y in range(moat_hi, moat_lo - 1, -1):
                    if m.at(k, y) == 'D': m.put(k, y, '=')
                    else: break
            if side == 'E':
                for x in range(moat_hi, moat_lo - 1, -1):
                    if m.at(x, k) == 'D': m.put(x, k, '=')
                    else: break


def snap(m, x, y, want):
    """Ближайшая свободная клетка нужного рода: задумку сдвигаем, а не ломаем."""
    for r in range(0, 6):
        best = None
        for dy in range(-r, r + 1):
            for dx in range(-r, r + 1):
                if max(abs(dx), abs(dy)) != r:
                    continue
                if m.at(x + dx, y + dy) in want:
                    d = abs(dx) + abs(dy)
                    if best is None or d < best[0]:
                        best = (d, x + dx, y + dy)
        if best:
            return best[1], best[2]
    return None


def economy(m, mines=(), farms=(), court=()):
    """Экономика ставится последней: ничто её уже не перекроет."""
    moved = []
    for kind, pts, want in (('M', list(mines), set('.B')),
                            ('F', list(farms), set('.B')),
                            ('M', list(court), set('C'))):
        for (x, y) in pts:
            p = snap(m, x, y, want)
            if p is None:
                moved.append((x, y, 'места нет'))
                continue
            if p != (x, y):
                moved.append((x, y, f'сдвинуто в {p[0]},{p[1]}'))
            m.put(p[0], p[1], kind)
    return moved


def lanes(m, spec):
    for (cx, cy, r, ch, d) in spec:
        blob(m, cx, cy, r, ch, density=d)


# --------------------------------------------------------------- варианты

COMMON = [
    'Река — рельеф с правилами рва: в воду не входит никто, пеший переходит только по настилу из фашин.',
    'Форпост у истока стоит поперёк ручья: он же верхняя переправа, и до него от обоих лагерей ровно одинаково.',
    'Запруда: кто держит форпост, тот управляет водой ниже по течению — это главная кнопка карты.',
]


def v1():
    m = Map('bend', 'Излучина', 'кольцо 60 сегментов · 2 ворот · река сквозь двор',
            'Квадратный замок стоит прямо на реке. Вода входит под северо-восточную '
            'арку, пересекает двор и уходит под юго-западную; над самым центром '
            'канала поднят донжон — трон стоит на мосту, как в Кагоре. Двор разрезан '
            'каналом, но через него переброшены ещё два моста, так что замок живёт '
            'как целое, пока они стоят. Ворот двое, по одним на осаждающего: '
            'западные и южные — то есть гати и запруда разнесены по карте, и осада '
            'идёт в двух не связанных между собой театрах.',
            COMMON + [
                'Водяные ворота (арка) — сегмент стены вроде ворот: пролом в нём впускает в замок по воде, минуя ров.',
                'Три моста во дворе по 60 прочности. Требушет бьёт их через стену — это новая осмысленная цель.',
                'Два моста в поле, выше и ниже замка: единственный путь на чужой берег в обход замка.',
            ],
            'Самый близкий к нынешним правилам: кольцо стены вместо линии, река как ров, '
            'форпост как нейтральная постройка. Переписывать придётся только привязку '
            'сегментов к столбцу (сейчас segment(p.X)).')
    lanes(m, [
        (9, 9, 3, 'B', .8), (15, 6, 2, 'B', .85), (6, 15, 2, 'B', .85),
        (19, 9, 2, 'B', .8), (9, 19, 2, 'B', .8),
        (4, 12, 2, 'V', .9), (12, 4, 2, 'V', .9),
        (9, 24, 3, 'B', .8), (24, 9, 3, 'B', .8),
        (33, 15, 2, 'B', .8), (15, 33, 2, 'B', .8),
        (36, 8, 2, 'V', .85), (8, 36, 2, 'V', .85),
        (31, 24, 2, 'B', .8), (24, 31, 2, 'B', .8),
        (39, 20, 2, 'R', .85), (20, 39, 2, 'R', .85),
    ])
    camps(m)
    m.fill(16, 27, 'C')
    m.ring(15, 28, 'W')
    wall_ring(m, 14, 29, gates=('W', 'S'))
    m.ring(13, 30, 'D')
    gate_bridges(m, 13, 30, ('W', 'S'))
    carve_river(m, 1, bridges=(0, 3, -3, 12, -12))
    outpost(m)
    bad = economy(m,
                  mines=[(10, 17), (17, 10), (32, 7)],
                  farms=[(24, 6), (6, 24), (29, 5)],
                  court=[(18, 21)])
    m.mark(21, 21, 'донжон на мосту')
    m.mark(14, 21, 'зап. ворота')
    return m, bad


def v2():
    m = Map('island', 'Остров', 'кольцо 52 сегмента · 2 гати · озеро вместо рва',
            'Река разливается вокруг замка озером: рва больше нет, вместо него три '
            'клетки воды по всему обводу. Двор при этом целый — вода обходит замок, '
            'а не рассекает его. К воротам ведут две гати, северная и восточная, и '
            'каждая простреливается со стены насквозь: это два длинных коридора '
            'смерти. Гати и форпост рядом, вверх по течению, поэтому вся осада '
            'стягивается в один северо-восточный театр.',
            COMMON + [
                'Озеро: без настила к стене не подойти вообще, а три клетки воды — это три действия фашинами под обстрелом.',
                'Запруда решает всё: перекрыл реку у форпоста — озеро падает, и отмель становится проходимой по всему обводу.',
                'Гати — земля, а не постройка: их не сломать, зато на них нет ни куста, ни укрытия.',
                'Замок компактный: 52 сегмента, защитнику хватает рук на обвод — но вылазки идут только по двум гатям.',
            ],
            'Самая дешёвая по коду: двор и стена остаются обычным кольцом, вся новизна — '
            'в ширине воды и в одном переключателе «запруда открыта/закрыта». Зато '
            'баланс рискованный: пока озеро полное, осада почти встала.')
    lanes(m, [
        (10, 10, 3, 'B', .8), (16, 7, 2, 'B', .85), (7, 16, 2, 'B', .85),
        (5, 13, 2, 'V', .9), (13, 5, 2, 'V', .9),
        (10, 22, 3, 'B', .8), (22, 10, 3, 'B', .8),
        (34, 16, 2, 'B', .8), (16, 34, 2, 'B', .8),
        (37, 9, 2, 'V', .85), (9, 37, 2, 'V', .85),
        (30, 26, 2, 'B', .8), (26, 30, 2, 'B', .8),
        (40, 21, 2, 'R', .85), (21, 40, 2, 'R', .85),
    ])
    camps(m)
    m.fill(17, 26, 'C')
    m.ring(16, 27, 'W')
    wall_ring(m, 15, 28, gates=('N', 'E'), tower_step=4)
    for lo, hi in ((14, 29), (13, 30), (12, 31)):
        m.ring(lo, hi, 'D')
    carve_river(m, 1, bridges=(0, 3, -3, 13, -13))
    for k in (21, 22):                     # гати: земля, а не постройка
        for i in range(12, 15):
            m.put(k, i, '.')               # северная (зеркало — восточная)
    outpost(m)
    bad = economy(m,
                  mines=[(11, 18), (18, 11), (33, 8)],
                  farms=[(25, 5), (5, 25), (30, 6)],
                  court=[(19, 21)])
    m.mark(21, 21, 'трон')
    m.mark(21, 13, 'сев. гать')
    return m, bad


# Опорные точки русла от истока до центра; вторую половину делает поворот.
# Петли нарочно разной длины: река должна читаться как река, а не как канал.
SPLIT_RIVER = [
    (43, 1),      # исток в северо-восточном углу
    (40, 2),
    (36, 2),      # первая петля жмётся к северу
    (33, 5),
    (33, 9),      # и уходит к юго-востоку
    (29, 11),     # свал к замку: один рукав, без слипшейся петли
    (26, 13),     # входит под северную стену, в трёх клетках от ворот
    (26, 17),     # во дворе река петляет к востоку
    (23, 20),
    (21.5, 21.5), # центр двора: здесь единственный мост
]


def v3():
    m = Map('split', 'Разделённый замок',
            'извилистое русло · 68 сегментов · 4 ворот · один мост',
            'Самый крупный замок — и единственная карта с извилистой рекой. Русло '
            'петляет от истока в северо-восточном углу, обходит замок с севера, '
            'входит под северную стену в шести клетках от ворот и, покружив по '
            'двору, уходит под южную. Двор от этого разрезан надвое — на западную '
            'половину и восточную, — а мост через канал всего один. Сломали его '
            'требушетом: гарнизон разорван, и каждая половина обороняется сама, '
            'подкрепление с одной стороны на другую не перейдёт. Поэтому ворот '
            'четверо, по двое на половину — иначе отрезанные остались бы вообще '
            'без выхода. Самая злая карта для защитника и самая разговорчивая для '
            'двух осаждающих: разрезать замок выгодно обоим, а вот входить в '
            'пролом — уже наперегонки.',
            COMMON + [
                'Один внутренний мост: пока цел — замок един, рухнул — две независимые половины.',
                'Четверо ворот: цена разрезанного двора. У каждой половины свой выход, и защищать надо всё.',
                'Водяные ворота под северной и южной стеной: пролом в арке впускает в замок по воде, минуя ров.',
                'Брандеры: с форпоста можно пускать горящие плоты вниз по течению — прямо в арку.',
                'Излучины дают берегам длину: мест, где можно выйти во фланг или спрятать требушет за изгибом, вдвое больше, чем у прямой реки.',
                '68 сегментов кладки против нынешних 20: на всю стену людей не хватит, защита становится выборочной.',
            ],
            'Самая интересная тактически и самая дорогая по коду: надо учить игру, что '
            'двор может быть разорван на две зоны (найм, снабжение, путь короля). '
            'Зато именно здесь река — не декорация, а ось всей партии. Отдельно: '
            'извилистое русло потребовало сменить род симметрии на поворот, и '
            'клетки такой карты придётся держать готовой таблицей, а не считать '
            'формулой от столбца, как сейчас.',
            sym='rot')
    lanes(m, [
        (8, 8, 3, 'B', .8), (14, 5, 2, 'B', .85), (5, 14, 2, 'B', .85),
        (18, 8, 2, 'B', .8), (8, 18, 2, 'B', .8),
        (3, 11, 2, 'V', .9), (11, 3, 2, 'V', .9),
        (8, 23, 3, 'B', .8), (23, 8, 3, 'B', .8),
        (34, 13, 2, 'B', .8), (13, 34, 2, 'B', .8),
        (37, 7, 2, 'V', .85), (7, 37, 2, 'V', .85),
        (33, 25, 2, 'B', .8), (25, 33, 2, 'B', .8),
        (40, 19, 2, 'R', .85), (19, 40, 2, 'R', .85),
    ])
    camps(m)
    blob(m, LAST - 3, 3, 3, 'R', density=.8)       # скалы у истока
    m.fill(15, 28, 'C')
    m.ring(14, 29, 'W')
    wall_ring(m, 13, 30, gates=('N', 'E', 'S', 'W'))
    m.ring(12, 31, 'D')
    gate_bridges(m, 12, 31, ('N', 'E', 'S', 'W'))
    m.crossings = {'court': 1, 'field': 2}
    carve_winding(m, SPLIT_RIVER, r=1.3,
                  bridges=((21.5, 21.5), (33, 7), (10, 36)),
                  post=(38, 3))
    # Донжон на опорах: каменная площадка в самом центре двора. Река идёт
    # под ней, и это единственный переход между половинами замка — его не
    # сломать требушетом, зато его можно запереть отрядами.
    for yy in range(20, 24):
        for xx in range(20, 24):
            m.g[yy][xx] = 'C'
    bad = economy(m,
                  mines=[(9, 16), (16, 9), (31, 6)],
                  farms=[(22, 4), (4, 22), (28, 4)],
                  court=[(17, 20)])
    m.mark(21, 21, 'донжон: единственный переход')
    m.mark(17, 25, 'трон')
    return m, bad


def v4():
    m = Map('gorge', 'Ущелье', 'плато · 44 сегмента · 4 подъёма · русло в обрывах',
            'Замок стоит на плато, обведённом обрывами: наверх ведут четыре подъёма, '
            'и колёсному обозу — только по ним. Река идёт в ущелье, берега '
            'обрывисты, фашинами их не засыпать: переправ на всю карту три, и '
            'верхняя — сам форпост. Карта большая, но дорог на ней мало, поэтому '
            'расстояние тратится на выбор пути, а не на пустой марш: обход есть '
            'всегда, только он стоит дней.',
            COMMON + [
                'Обрывы по обводу плато: наверх — только четырьмя подъёмами, и это узкие горлышки для таранов и башен.',
                'Русло в обрывистых берегах: засыпать его нельзя, переправа только по мостам и через форпост.',
                'Компактный замок, 44 сегмента: защитнику хватает рук, и осада решается подъёмами, а не длиной кладки.',
                'Форпост — господствующая высота: с него видно оба берега и верхний подъём.',
            ],
            'Лучше всех решает главную проблему большой карты — пустой марш. Зато '
            'самая тесная: если горлышки окажутся слишком узкими, осада встанет в '
            'очередь из таранов. Требует настройки ширины подъёмов на живой партии.')
    lanes(m, [
        (7, 7, 3, 'B', .8), (13, 4, 2, 'B', .85), (4, 13, 2, 'B', .85),
        (19, 7, 2, 'B', .8), (7, 19, 2, 'B', .8),
        (8, 25, 3, 'B', .8), (25, 8, 3, 'B', .8),
        (35, 14, 2, 'B', .8), (14, 35, 2, 'B', .8),
        (33, 27, 2, 'B', .8), (27, 33, 2, 'B', .8),
        (41, 18, 2, 'R', .85), (18, 41, 2, 'R', .85),
    ])
    camps(m)
    for lo, hi in ((11, 32), (12, 31)):
        m.ring(lo, hi, 'V')
    for k in (21, 22):                     # четыре подъёма на плато
        for i in range(11, 13):
            m.put(k, i, '^'); m.put(k, 43 - i, '^')
            m.put(i, k, '^'); m.put(43 - i, k, '^')
    m.fill(18, 25, 'C')
    m.ring(17, 26, 'W')
    wall_ring(m, 16, 27, gates=('W', 'S'), tower_step=3)
    m.ring(15, 28, 'D')
    gate_bridges(m, 15, 28, ('W', 'S'))
    carve_river(m, 1, canyon=True, bridges=(0, 15, -15))
    outpost(m)
    bad = economy(m,
                  mines=[(8, 18), (18, 8), (34, 11)],
                  farms=[(26, 6), (6, 26), (31, 7)],
                  court=[(19, 22)])
    m.mark(21, 21, 'донжон на мосту')
    m.mark(21, 12, 'подъём')
    return m, bad


# --------------------------------------------------------------- проверки

BushMoveCost = 2


def dijkstra(m, from_ch, costs):
    """Ход считается очками действия: поле 1, куст 2."""
    INF = float('inf')
    dist = [[INF] * N for _ in range(N)]
    buckets = {0: []}
    for y in range(N):
        for x in range(N):
            if m.g[y][x] == from_ch:
                dist[y][x] = 0
                buckets[0].append((x, y))
    d = 0
    while buckets:
        if d not in buckets:
            d += 1
            if d > 4 * N:
                break
            continue
        for (x, y) in buckets.pop(d):
            if dist[y][x] != d:
                continue
            for dx in (-1, 0, 1):
                for dy in (-1, 0, 1):
                    if dx == dy == 0:
                        continue
                    nx, ny = x + dx, y + dy
                    if not (0 <= nx < N and 0 <= ny < N):
                        continue
                    c = costs.get(m.g[ny][nx])
                    if c is None:
                        continue
                    if d + c < dist[ny][nx]:
                        dist[ny][nx] = d + c
                        buckets.setdefault(d + c, []).append((nx, ny))
        d += 1
        if d > 4 * N:
            break
    return dist


def nearby(dist, p):
    """Сколько ОД до клетки: считаем подход вплотную, а не вход в неё."""
    x, y = p
    best = float('inf')
    for dx in (-1, 0, 1):
        for dy in (-1, 0, 1):
            nx, ny = x + dx, y + dy
            if 0 <= nx < N and 0 <= ny < N:
                best = min(best, dist[ny][nx])
    return best


def bfs(m, start, allowed):
    seen, q = {start}, collections.deque([start])
    while q:
        x, y = q.popleft()
        for dx in (-1, 0, 1):
            for dy in (-1, 0, 1):
                if dx == dy == 0:
                    continue
                nx, ny = x + dx, y + dy
                if 0 <= nx < N and 0 <= ny < N and (nx, ny) not in seen \
                   and m.g[ny][nx] in allowed:
                    seen.add((nx, ny)); q.append((nx, ny))
    return seen


def check(m, bad):
    errs = []
    for (x, y, how) in bad:
        if how == 'места нет':
            errs.append(f'экономика не встала: {x},{y}')
    same = {'a': 'b', 'b': 'a'}
    for y in range(N):
        for x in range(N):
            mx, my = m.twin(x, y)
            a, b = m.g[y][x], m.g[my][mx]
            if 'O' in (a, b):
                continue          # форпост один на карту, пары у него нет
            if a != b and same.get(a) != b:
                errs.append(f'асимметрия {x},{y}={a} против {mx},{my}={b}')
                break
        if errs and errs[-1].startswith('асим'):
            break
    water = {(x, y) for y in range(N) for x in range(N) if m.g[y][x] in '~g=D'}
    if (LAST, 0) not in water or (0, LAST) not in water:
        errs.append('река не доходит до углов карты')
    # Река должна быть настоящей преградой: убираем переправы и форпост —
    # берега обязаны разойтись. Иначе русло перепрыгивают по диагонали.
    dry = Map('dry', '', '', '', [], '', sym=m.sym)
    dry.g = [row[:] for row in m.g]
    for y in range(N):
        for x in range(N):
            if dry.g[y][x] in ('=', 'O'):
                dry.g[y][x] = '~'
    if (LAST - 2, LAST - 2) in bfs(dry, (2, 2), PASSABLE):
        errs.append('русло дырявое: берега сходятся без переправ')

    reach = {}
    for camp, start in (('A', (2, 2)), ('B', (LAST - 2, LAST - 2))):
        seen = bfs(m, start, PASSABLE)
        reach[camp] = seen
        gates = [(x, y) for y in range(N) for x in range(N) if m.g[y][x] == 'G']
        touch = [g for g in gates
                 if any((g[0] + dx, g[1] + dy) in seen
                        for dx in (-1, 0, 1) for dy in (-1, 0, 1))]
        if not touch:
            errs.append(f'лагерь {camp}: ни к одним воротам не подойти по суше')
        if not any(m.g[y][x] == 'O' for (x, y) in seen):
            errs.append(f'лагерь {camp}: форпост недостижим')
        other = (LAST - 2, LAST - 2) if camp == 'A' else (2, 2)
        if other not in seen:
            errs.append(f'лагерь {camp}: нет пути к чужому лагерю')
    posts = sum(row.count('O') for row in m.g)
    if abs(len(reach['A']) - len(reach['B'])) > posts:
        errs.append(f"разный доступный мир: A={len(reach['A'])}, B={len(reach['B'])}")

    court_box = max((max(abs(x - 21.5), abs(y - 21.5))
                     for y in range(N) for x in range(N) if m.g[y][x] == 'C'),
                    default=0)
    inner = [(x, y) for y in range(N) for x in range(N) if m.g[y][x] in 'CWG=' and
             max(abs(x - 21.5), abs(y - 21.5)) <= court_box + 1]
    if inner:
        seen = bfs(m, inner[0], set('CWG='))
        seen = {p for p in seen if p in set(inner)}
        if len(seen) != len(inner):
            errs.append(f'двор разорван: связно {len(seen)} из {len(inner)}')
    # Честность меряем не по прямой, а ходами: Дейкстра с ценой входа.
    costs = {'.': 1, 'B': BushMoveCost, 'M': 1, 'F': 1, '=': 1, 'O': 1,
             '^': 1, 'a': 1, 'b': 1}
    apA, apB = dijkstra(m, 'a', costs), dijkstra(m, 'b', costs)
    for kind, label in (('M', 'рудникам'), ('F', 'пашням'),
                        ('G', 'воротам'), ('O', 'форпосту')):
        pts = [(x, y) for y in range(N) for x in range(N) if m.g[y][x] == kind]
        if not pts:
            continue
        da = sorted(nearby(apA, p) for p in pts)
        db = sorted(nearby(apB, p) for p in pts)
        if kind == 'O':
            d = abs(min(da) - min(db))
            m.fair_post = (min(da), min(db))
            if d > 1:
                errs.append(f'форпост ближе одному из лагерей на {d} ОД '
                            f'(A {min(da)}, B {min(db)})')
        elif da != db:
            errs.append(f'нечестно по {label}: наборы расстояний разные')
    return errs


def stats(m):
    c = collections.Counter(ch for row in m.g for ch in row)
    return {'segments': c['#'] + c['T'] + c['G'] + c['g'], 'gates': c['G'] // 2,
            'arches': c['g'], 'court': c['C'], 'water': c['~'] + c['D'],
            'bridges': c['='], 'bush': c['B'], 'ravine': c['V'], 'rock': c['R'],
            'mines': c['M'], 'farms': c['F'], 'camp': c['a']}


if __name__ == '__main__':
    out = []
    for fn in (v1, v2, v3, v4):
        m, bad = fn()
        errs = check(m, bad)
        st = stats(m)
        print(f"== {m.name} ({m.key}) ==")
        print('   ', st)
        for e in errs:
            print('    ОШИБКА:', e)
        if not errs:
            print('    все проверки пройдены')
        out.append({'key': m.key, 'name': m.name, 'tag': m.tag, 'note': m.note,
                    'mechanics': m.mechanics, 'tradeoff': m.tradeoff,
                    'marks': m.marks, 'stats': st, 'crossings': m.crossings,
                    'sym': m.sym, 'fairPost': getattr(m, 'fair_post', None),
                    'rows': [''.join(r) for r in m.g]})
    base = os.path.dirname(os.path.abspath(__file__)) + '/'
    with open(base + 'variants.json', 'w') as f:
        json.dump({'n': N, 'variants': out}, f, ensure_ascii=False)
    with open(base + 'variants.txt', 'w') as f:
        for v in out:
            f.write(f"=== {v['name']} === {v['tag']}\n")
            f.write('    ' + ''.join(str(x % 10) for x in range(N)) + '\n')
            for i, r in enumerate(v['rows']):
                f.write(f'{i:>3} ' + r + '\n')
            f.write('\n')
    print('готово')
