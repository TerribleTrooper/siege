#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Пересчитывает «Разделённый замок» в Go-файл с данными карты.

    python3 tools/map/emit.py       # обновить internal/game/board_split.go

Карта считается, а не рисуется руками: симметрия, проходимость берегов и
равные расстояния от обоих лагерей проверяются тут же, в maps.py, и в Go
уезжает уже проверенная сетка.
"""
import os, sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)
import maps

m, bad = maps.v3()
errs = maps.check(m, bad)
assert not errs, errs
N = maps.N
g = [''.join(r) for r in m.g]

def on_moat_ring(x, y):
    return max(abs(x - 21.5), abs(y - 21.5)) == 9.5

# Рельеф под настилами и форпостом: сам настил — постройка, а не клетка.
terrain = []
decks, forts = [], []
for y in range(N):
    row = []
    for x in range(N):
        c = g[y][x]
        if c in '=O':
            if c == '=':
                decks.append((x, y))
            else:
                forts.append((x, y))
            if on_moat_ring(x, y):
                c = 'D'
            elif (x, y) in m.river:
                c = '~'
            else:
                c = '.'
        row.append(c)
    terrain.append(''.join(row))

mines = [(x, y) for y in range(N) for x in range(N) if terrain[y][x] == 'M']
# Полевые рудники вперёд, дворовые следом: по этому порядку подписан интерфейс.
inside_box = lambda x, y: 13 <= x <= 30 and 13 <= y <= 30
mines.sort(key=lambda p: (inside_box(*p), p[1], p[0]))
farms = [(x, y) for y in range(N) for x in range(N) if terrain[y][x] == 'F']

def fmt_pts(pts):
    return ', '.join('{%d, %d}' % (x, y) for (x, y) in pts)

rows = '\n'.join('\t"%s",' % r for r in terrain)

out = f'''package game

// Карта «Разделённый замок»: замок посреди поля, осаждающие в противоположных
// углах, река сквозь двор, форпост у истока.
//
// Карта и все числа ниже посчитаны заранее и проверены: симметрия поворотом
// на пол-оборота вокруг замка (мир одного осаждающего — точная копия мира
// другого), берега расходятся без переправ, до ближайших ворот от обоих
// лагерей поровну. Править её руками здесь не стоит — проще пересчитать.
//
// Почему симметрия поворотом, а не зеркалом, как на «Теснине»: в координатах
// вдоль оси (u поперёк течения, v вдоль) зеркало переводит (u,v) в (-u,v) и
// потому требует строго прямого русла. Поворот переводит (u,v) в (-u,-v) и
// допускает излучину — если каждый изгиб к северо-западу отвечает такому же
// изгибу к юго-востоку.
//
// Обозначения строк: . поле, B кусты, V обрыв, R скалы, M рудник, F пашня,
// D ров, ~ река, C двор, W боевой ход, # стена, T башня, G ворота,
// g водяные ворота (арка), a/b лагеря осаждающих.
var splitRows = []string{{
{rows}
}}

// Настилы, стоящие с первого дня: мосты у четырёх ворот и две переправы в
// поле. Форпост у истока — отдельно: он прочнее и он же запруда.
var splitDecks = []Point{{{fmt_pts(decks)}}}

var splitFort = []Point{{{fmt_pts(forts)}}}

var Split = buildSplit()

func buildSplit() *Board {{
	b := &Board{{
		Key: "split", Name: "Разделённый замок",
		Cols: {N}, Rows: {N},
		segAt: map[Point]int{{}}, segKind: map[Point]SegmentKind{{}},
		inside: map[Point]bool{{}}, campOf: map[Point]Side{{}},
		workshops: map[Side]Point{{}},
	}}
	b.tiles = make([]TileKind, b.Cols*b.Rows)
	kinds := map[byte]TileKind{{
		'.': TileField, 'B': TileBush, 'V': TileRavine, 'R': TileCliff,
		'M': TileMine, 'F': TileFarm, 'D': TileMoat, '~': TileRiver,
		'C': TileCourtyard, 'W': TileWalk, 'a': TileCamp, 'b': TileCamp,
		'#': TileWall, 'T': TileWall, 'G': TileWall, 'g': TileWall,
	}}
	segKind := map[byte]SegmentKind{{
		'#': SegWall, 'T': SegTower, 'G': SegGate, 'g': SegArch,
	}}
	for y := 0; y < b.Rows; y++ {{
		for x := 0; x < b.Cols; x++ {{
			p := Point{{x, y}}
			ch := splitRows[y][x]
			b.tiles[y*b.Cols+x] = kinds[ch]
			switch ch {{
			case 'a':
				b.campOf[p] = SideAttackerA
			case 'b':
				b.campOf[p] = SideAttackerB
			}}
			if k, ok := segKind[ch]; ok {{
				b.segAt[p] = len(b.segs)
				b.segKind[p] = k
				b.segs = append(b.segs, p)
				if k == SegGate {{
					b.gates = append(b.gates, p)
				}}
			}}
			if kinds[ch].Water() {{
				b.water = append(b.water, p)
			}}
		}}
	}}
	// Внутри замка — всё, что кольцо кладки обводит: двор, боевой ход и
	// вода под арками. Заливкой от трона: так в «нутро» не попадёт ни ров
	// снаружи, ни река выше по течению.
	b.throne = Point{{21, 21}}
	b.floodInside(b.throne)
	for i, p := range b.segs {{
		_ = i
		b.pitch = append(b.pitch, b.outward(p))
	}}
	b.mines = []Point{{{fmt_pts(mines)}}}
	b.farms = []Point{{{fmt_pts(farms)}}}
	b.decks = splitDecks
	b.fort = splitFort
	// Казармы по одной в каждой половине двора: половины живут порознь, и
	// найм должен быть у каждой свой.
	b.barracks = []Point{{{{17, 17}}, {{26, 26}}}}
	b.workshops[SideAttackerA] = Point{{3, 3}}
	b.workshops[SideAttackerB] = Point{{40, 40}}
	// Стартовый гарнизон — по отряду за каждыми воротами.
	b.garrison = []Point{{{{21, 14}}, {{29, 21}}, {{22, 29}}, {{14, 22}}}}
	return b
}}

// floodInside размечает нутро замка заливкой от трона: двор, боевой ход и
// вода, которую обводит кладка. Сквозь кладку заливка не идёт, поэтому ров
// снаружи и река выше по течению внутрь не попадают.
func (b *Board) floodInside(from Point) {{
	queue := []Point{{from}}
	b.inside[from] = true
	for len(queue) > 0 {{
		p := queue[0]
		queue = queue[1:]
		for _, d := range neighborOffsets {{
			n := p.Add(d[0], d[1])
			if !b.In(n) || b.inside[n] || b.TileAt(n) == TileWall {{
				continue
			}}
			switch b.TileAt(n) {{
			case TileCourtyard, TileWalk, TileMoat, TileRiver, TileMine, TileField:
				b.inside[n] = true
				queue = append(queue, n)
			}}
		}}
	}}
}}

// outward — клетка под сегментом снаружи: туда льют смолу. Сторона считается
// от середины замка, поэтому на кольце это работает так же, как на прямой
// стене «Теснины».
func (b *Board) outward(p Point) Point {{
	cx, cy := float64(b.Cols-1)/2, float64(b.Rows-1)/2
	sx, sy := signf(float64(p.X)-cx), signf(float64(p.Y)-cy)
	for _, d := range [][2]int{{{{sx, 0}}, {{0, sy}}, {{sx, sy}}}} {{
		n := p.Add(d[0], d[1])
		if b.In(n) && b.TileAt(n) != TileWall && !b.inside[n] {{
			return n
		}}
	}}
	return p
}}

func signf(v float64) int {{
	if v > 0 {{
		return 1
	}}
	if v < 0 {{
		return -1
	}}
	return 0
}}
'''
out_path = os.path.normpath(os.path.join(HERE, '..', '..', 'internal', 'game', 'board_split.go'))
open(out_path, 'w').write(out)
print('записано:', out_path)
print('рудники:', mines)
print('пашни:', farms)
print('настилы:', len(decks), 'форпост:', len(forts))
