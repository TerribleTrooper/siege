package bot

import "siege/internal/game"

// Поиск короля. Пехота и рыцари видят только соседнюю клетку, и за стеной
// короля почти никогда не видно издали. Поэтому бот держит в памяти то же,
// что держал бы в голове человек: круг мест, где король может быть, и когда
// он последний раз смотрел на каждую клетку замка.
//
// В начале осады круг — это трон. За каждый ход замка он расширяется на
// шаг короля, а всё, что бот видит пустым, из круга вычёркивается. Круг —
// честная граница: короля нет нигде, кроме него. Но по нему одному не
// найти: за ход замка он отрастает во все стороны и снова заливает только
// что осмотренное. Поэтому отряды идут к тем клеткам круга, куда дольше
// всего никто не смотрел, — каждый к ближайшей такой, — и прочёсывают двор,
// а не топчутся на месте.
//
// Без этой памяти бот искал «ближайшую невидимую клетку к трону»: стоило
// отряду отойти, осмотренное снова уходило в туман, и толпа часами кружила
// вокруг пустого трона, пока король стоял в углу двора.

// trackKing обновляет круг по снимку. Видно короля — круг сжимается в одну
// клетку; круг опустел — король ушёл туда, куда бот не смотрел, и искать
// приходится по всему невидимому замку.
func (b *Bot) trackKing(w *world) {
	m := &b.mem
	if m.maybeDay == 0 {
		m.maybe = grid[bool]{}
		t := game.ThroneTile()
		m.maybe[t.Y][t.X] = true
	}
	// Замок ходит первым в каждом дне: к ходу осаждающего в день d король
	// успел сделать d ходов, по шагу короля за ход.
	for m.maybeDay < w.v.Day {
		for i := 0; i < game.Spec(game.UnitKing).Move; i++ {
			m.maybe = w.spread(&m.maybe)
		}
		m.maybeDay++
	}
	for y := range m.seen {
		for x := range m.seen[y] {
			if w.visible(P{X: x, Y: y}) {
				m.seen[y][x] = w.v.Day
			}
		}
	}
	for _, u := range w.foes {
		if u.spec.Royal {
			m.maybe = grid[bool]{}
			m.maybe[u.Pos.Y][u.Pos.X] = true
			return
		}
	}
	left := false
	for y := range m.maybe {
		for x := range m.maybe[y] {
			if m.maybe[y][x] && w.visible(P{X: x, Y: y}) {
				m.maybe[y][x] = false
			}
			left = left || m.maybe[y][x]
		}
	}
	if left {
		return
	}
	for y := range m.maybe {
		for x := range m.maybe[y] {
			p := P{X: x, Y: y}
			if _, ok := w.enter(kingClass, p); ok && !w.visible(p) {
				m.maybe[y][x] = true
			}
		}
	}
}

// kingClass — как ходит король: только по замку и не сквозь постройки.
var kingClass = mclass{bound: true}

// spread — куда король мог дойти за один шаг из клеток круга. Отряды на
// пути не учитываются: лучше искать шире, чем потерять короля.
func (w *world) spread(g *grid[bool]) grid[bool] {
	out := *g
	for y := range g {
		for x := range g[y] {
			if !g[y][x] {
				continue
			}
			for _, o := range dirs {
				if p := (P{X: x + o[0], Y: y + o[1]}); w.in(p) {
					if _, ok := w.enter(kingClass, p); ok {
						out[p.Y][p.X] = true
					}
				}
			}
		}
	}
	return out
}

// kingMaybe — круг списком.
func (b *Bot) kingMaybe() []P {
	var out []P
	for y := range b.mem.maybe {
		for x := range b.mem.maybe[y] {
			if b.mem.maybe[y][x] {
				out = append(out, P{X: x, Y: y})
			}
		}
	}
	return out
}

// searchSeeds — куда идти искать короля. Чем дольше на клетку круга никто не
// смотрел, тем вернее король там: только что осмотренное он мог занять разве
// что за последний ход. Рядом со стражей — первым делом: храмовники от
// короля не отходят.
func (b *Bot) searchSeeds(w *world, maybe []P) []seed {
	out := make([]seed, 0, len(maybe))
	for _, p := range maybe {
		d := 0
		if !w.guarded(p) {
			if last := b.mem.seen[p.Y][p.X]; last > 0 {
				d = max(0, 4*(4-(w.v.Day-last)))
			}
		}
		out = append(out, seed{p, d})
	}
	return out
}

// middle — клетка круга, ближайшая к его середине: куда целиться, пока
// короля не видно (например, какой участок стены ломать).
func middle(ps []P) P {
	if len(ps) == 0 {
		return game.ThroneTile()
	}
	sx, sy := 0, 0
	for _, p := range ps {
		sx, sy = sx+p.X, sy+p.Y
	}
	c := P{X: (sx + len(ps)/2) / len(ps), Y: (sy + len(ps)/2) / len(ps)}
	best := ps[0]
	for _, p := range ps {
		if game.Chebyshev(p, c) < game.Chebyshev(best, c) {
			best = p
		}
	}
	return best
}

// guarded — стоит ли рядом с клеткой королевская стража. Храмовники от
// короля не отходят, и где видна стража, там вернее всего и он.
func (w *world) guarded(p P) bool {
	for _, e := range w.foes {
		if e.typ == game.UnitTemplar && game.Chebyshev(e.Pos, p) <= 1 {
			return true
		}
	}
	return false
}
