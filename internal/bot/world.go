package bot

import (
	"container/heap"
	"fmt"
	"sort"

	"siege/internal/game"
)

// Модель мира, собранная из снимка. Всё, что бот знает о партии, он берёт
// отсюда, а сюда попадает только снимок его места: туман и засады вырезаны
// сервером ещё до того, как бот их увидел бы.

type P = game.Point

const inf = 1 << 20

// dirs — восемь направлений: юниты ходят по квадрату.
var dirs = [8][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}, {-1, -1}, {1, -1}, {-1, 1}, {1, 1}}

// unit — отряд из снимка вместе с его неизменной характеристикой.
type unit struct {
	*game.UnitView
	typ  game.UnitType
	spec game.UnitSpec
	side game.Side
}

// maxDim — сторона самой большой карты в игре. Сетка берётся с запасом под
// неё: так она остаётся массивом (а он вчетверо быстрее словаря, и путь бот
// пересчитывает на каждом шаге), но подходит обеим картам.
const maxDim = 44

// grid — что-то по клетке поля.
type grid[T any] [maxDim][maxDim]T

func (g *grid[T]) at(p P) (v T) {
	if p.X >= 0 && p.X < maxDim && p.Y >= 0 && p.Y < maxDim {
		v = g[p.Y][p.X]
	}
	return v
}

// in — внутри ли точка той карты, на которой идёт партия.
func (w *world) in(p P) bool { return w.b.In(p) }

func (w *world) tile(p P) game.TileKind { return w.b.TileAt(p) }

func (w *world) inBush(p P) bool { return w.tile(p) == game.TileBush }

func (w *world) insideCastle(p P) bool {
	t := w.tile(p)
	return t == game.TileWalk || t == game.TileCourtyard
}

type world struct {
	v      *game.View
	side   game.Side
	own    []*unit     // свои, кроме пассажиров осадной башни
	foes   []*unit     // видимые враги
	allies []*unit     // второй осаждающий: союзник по правилам
	b      *game.Board // карта партии: их две, и рельеф у них разный
	units  grid[*unit]
	blds   grid[*game.BuildingView]
	water  grid[bool] // открытая вода во рву
	seg    map[P]*game.WallSegment
	buy    map[string]*game.BuyView
	fields map[string]*field
	costs  map[mclass]*grid[int] // во что обходится вход в клетку, -1 — нельзя
	// boost — считать свои удары так, будто войска уже воодушевлены:
	// так бот прикидывает, окупится ли заклинание.
	boost bool
}

func newWorld(v *game.View) *world {
	w := &world{
		v: v, side: game.SideByName(v.You.Side), b: game.BoardByKey(v.Map),
		buy: map[string]*game.BuyView{}, fields: map[string]*field{},
		costs: map[mclass]*grid[int]{}, seg: map[P]*game.WallSegment{},
	}
	for i := range v.Units {
		uv := &v.Units[i]
		t, ok := game.UnitTypeByName(uv.Type)
		if !ok {
			continue
		}
		u := &unit{UnitView: uv, typ: t, spec: game.Spec(t), side: game.SideByName(uv.Side)}
		w.units[uv.Pos.Y][uv.Pos.X] = u
		switch {
		case uv.Mine:
			w.own = append(w.own, u)
		case hostile(w.side, u.side):
			w.foes = append(w.foes, u)
		default:
			w.allies = append(w.allies, u)
		}
	}
	for i := range v.Buildings {
		b := &v.Buildings[i]
		w.blds[b.Pos.Y][b.Pos.X] = b
	}
	for i := range v.Wall {
		s := &v.Wall[i]
		w.seg[s.Pos] = s
	}
	for _, p := range v.Moat {
		if w.in(p) {
			w.water[p.Y][p.X] = true
		}
	}
	for i := range v.Buyable {
		w.buy[v.Buyable[i].Unit] = &v.Buyable[i]
	}
	return w
}

// hostile — враги ли стороны. Осаждающие друг другу не враги: так велит
// обычай, и сервер всё равно не даст им драться.
// targetBuilding — стоит ли бить по этой постройке. Своё и союзное не трогаем,
// а мост у ворот ничей: для замка это цель — сломал и вернул воду под стену,
// — а для осады наоборот, её единственная готовая переправа.
func (w *world) targetBuilding(b *game.BuildingView) bool {
	if b.Kind == "bridge" {
		return w.side == game.SideDefender
	}
	return hostile(w.side, game.SideByName(b.Side))
}

func hostile(a, b game.Side) bool {
	if a == b || a == game.SideNeutral || b == game.SideNeutral {
		return false
	}
	return !(a.IsAttacker() && b.IsAttacker())
}

func (w *world) unitAt(p P) *unit             { return w.units.at(p) }
func (w *world) bldAt(p P) *game.BuildingView { return w.blds.at(p) }
func (w *world) wet(p P) bool                 { return w.water.at(p) }

func (w *world) visible(p P) bool {
	if !w.in(p) || p.Y >= len(w.v.Fog) {
		return false
	}
	row := w.v.Fog[p.Y]
	return p.X < len(row) && row[p.X] == '1'
}

func (w *world) inTower(p P) bool {
	b := w.bldAt(p)
	return b != nil && b.Kind == "tower"
}

// intact — стоит ли сегмент в этом столбце.
// opening — проходим ли сегмент: пролом или поднятая решётка.
// count — сколько своих отрядов этого вида. Пассажиры башни в снимке на
// карте не стоят, но в армии они есть — их считаем по её экипажу.
func (w *world) count(t game.UnitType) int {
	n := 0
	for _, u := range w.own {
		if u.typ == t {
			n++
		}
		for _, c := range u.Cargo {
			if c.Type == t.String() {
				n++
			}
		}
	}
	return n
}

// --- урон ---

// power — урон отряда по живой цели до укрытия: тип, звёзды, вышка и
// воодушевление. Повторяет unitDamage сервера.
// siege — сколько отряд снимает с кладки или постройки. Зеркало
// State.wallDamage: у катапульты камень крошит кладку вдвое медленнее, чем
// бьёт по людям, и в оценке обстрела это должно быть то же число.
func (w *world) siege(u *unit) int {
	if u.spec.SiegeDamage > 0 {
		return w.powerFrom(u, u.spec.SiegeDamage)
	}
	d := w.power(u)
	if u.spec.VsWall > 0 {
		d *= u.spec.VsWall
	}
	return d
}

func (w *world) power(u *unit) int {
	return w.powerFrom(u, u.spec.Damage)
}

func (w *world) powerFrom(u *unit, base int) int {
	d := base
	if d <= 0 {
		return 0
	}
	d += u.Stars * game.VeteranDamage
	if u.spec.Range > 1 && w.inTower(u.Pos) {
		d += game.TowerDamageBonus
	}
	if u.Mine && (w.v.You.Rally || w.boost) {
		d *= game.RallyFactor
	}
	return d
}

// cover — укрытие цели от дальнего удара: кусты и вышка.
func (w *world) cover(dmg int, to P, sp game.UnitSpec, dist int) int {
	if dist <= 1 || sp.Range <= 1 {
		return dmg
	}
	cut := 0
	switch {
	case w.inTower(to):
		cut = game.TowerCoverCut
	case w.inBush(to):
		cut = game.BushCoverCut
	default:
		return dmg
	}
	if left := dmg - cut; left > game.MinRangedHit {
		return left
	}
	return game.MinRangedHit
}

// shield — мантелет на линии выстрела, который примет удар вместо цели.
func (w *world) shield(t *unit, from P) *unit {
	if game.Chebyshev(from, t.Pos) <= 1 {
		return nil
	}
	m := w.unitAt(t.Pos.Add(sign(from.X-t.Pos.X), sign(from.Y-t.Pos.Y)))
	if m == nil || m.typ != game.UnitMantlet || hostile(m.side, t.side) {
		return nil
	}
	return m
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// reachOf — дальность удара: в снимке она уже с поправкой на вышки.
func reachOf(u *unit) int {
	if u.Reach > 0 {
		return u.Reach
	}
	return u.spec.Range
}

// canStrike — достанет ли враг клетку p на своём следующем ходу: шаг и
// удар. Отряд за стеной ходит только вдоль неё — наружу ему не выйти, — а
// машины без хода бьют только со своего места.
func (w *world) canStrike(e *unit, p P) bool {
	if e.spec.Damage <= 0 || e.spec.NoUnits {
		return false
	}
	r := reachOf(e) + e.Area
	mv := e.spec.Move
	dx, dy := abs(p.X-e.Pos.X), abs(p.Y-e.Pos.Y)
	if w.insideCastle(e.Pos) && !w.insideCastle(p) {
		return dx <= mv+r && dy <= r
	}
	if e.spec.Bound && !w.insideCastle(p) {
		return false
	}
	return max(dx, dy) <= mv+r
}

// threat — сколько урона, по видимым врагам, может прилететь в клетку на
// их следующем ходу. Враги в тумане сюда не попадают: бот рискует ровно
// так же, как рисковал бы человек.
func (w *world) threat(p P, victim *unit) int {
	sum := 0
	for _, e := range w.foes {
		if !w.canStrike(e, p) {
			continue
		}
		ranged := e.spec.Range > 1 || e.Area > 0
		if victim != nil && victim.spec.MeleeOnly && ranged {
			continue
		}
		dmg := e.spec.Damage + e.Stars*game.VeteranDamage
		if victim != nil && victim.spec.Engine {
			dmg *= max(e.spec.VsEngine, 1)
		}
		sum += w.cover(dmg, p, e.spec, 2)
	}
	// Под самой стеной осаждающего ждёт ещё и смола: её не видно заранее,
	// но котлы на стене есть всегда.
	if w.side.IsAttacker() && p.Y >= game.WallRow && p.Y <= game.WallRow+2 &&
		p.X >= game.WallFirstCol-1 && p.X <= game.WallLastCol+1 {
		sum += game.PitchDamage
	}
	return sum
}

// buildingSight — обзор построек: те же числа, что у сервера.
var buildingSight = map[string]int{
	"barracks": game.VisionBarracks, "workshop": game.VisionWorkshop, "tower": game.VisionTower,
	"barricade": game.VisionBarricade, "outpost": game.VisionOutpost, "arrowTower": game.VisionTower,
	"forge": game.VisionWorkshop, "warehouse": game.VisionBarracks,
}

// watched — смотрит ли на клетку кто-нибудь из видимых врагов. Нужно,
// чтобы прикинуть, спрячут ли кусты: в заросли уходят незамеченными, а
// под чужим взглядом они уже не помогут.
func (w *world) watched(p P) bool {
	switch {
	case w.side.IsAttacker() && p.Y <= game.WallRow:
		return true // замок видит себя целиком
	case w.side == game.SideDefender && w.b.CampSide(p) != game.SideNeutral:
		return true // а осаждающие — свои лагеря
	}
	for _, e := range w.foes {
		v := e.spec.Vision
		if e.spec.Range > 1 && w.inTower(e.Pos) {
			v += game.TowerVisionBonus
		}
		if game.Chebyshev(e.Pos, p) <= v {
			return true
		}
	}
	for _, b := range w.v.Buildings {
		owner := game.SideByName(b.Side)
		if b.Kind == "tower" && b.Hold != "" {
			owner = game.SideByName(b.Hold)
		}
		if hostile(w.side, owner) && game.Chebyshev(b.Pos, p) <= buildingSight[b.Kind] {
			return true
		}
	}
	return false
}

// --- поиск пути ---

// mclass — как ходит отряд: машины на катках не лезут в воду, колёсные
// разворачиваются на диагонали вдвое дольше, король и стража не выходят из
// замка, а громоздкое не входит в вышку.
type mclass struct{ rolls, wheeled, bound, noTower bool }

func classOf(sp game.UnitSpec) mclass {
	return mclass{rolls: sp.Rolls, wheeled: sp.Wheeled, bound: sp.Bound, noTower: sp.NoTower}
}

var footClass = mclass{}

// enter — во что обходится вход в клетку и можно ли в неё войти вообще,
// не считая стоящих на ней отрядов. Считается один раз на шаг для каждого
// вида хода.
func (w *world) enter(mc mclass, p P) (int, bool) {
	if !w.in(p) {
		return 0, false
	}
	g := w.costs[mc]
	if g == nil {
		g = w.costGrid(mc)
		w.costs[mc] = g
	}
	c := g[p.Y][p.X]
	return c, c >= 0
}

func (w *world) costGrid(mc mclass) *grid[int] {
	g := &grid[int]{}
	// Клетки за краем карты партии закрыты наглухо: сетка берётся с запасом
	// под самую большую карту, и незаполненный хвост иначе читался бы как
	// «пройти даром».
	for y := 0; y < maxDim; y++ {
		for x := 0; x < maxDim; x++ {
			g[y][x] = -1
		}
	}
	for y := 0; y < w.b.Rows; y++ {
		for x := 0; x < w.b.Cols; x++ {
			g[y][x] = w.enterCost(mc, P{X: x, Y: y})
		}
	}
	return g
}

func (w *world) enterCost(mc mclass, p P) int {
	if mc.bound && !w.insideCastle(p) {
		return -1
	}
	switch w.tile(p) {
	case game.TileCliff, game.TileRavine:
		return -1
	case game.TileWall:
		if !w.openingAt(p) {
			return -1
		}
	}
	if b := w.bldAt(p); b != nil {
		switch b.Kind {
		case "fascine":
		case "tower":
			if mc.noTower {
				return -1
			}
		default:
			return -1
		}
	}
	// Открытая вода непроходима для всех: к стене подходят только по
	// настилу из фашин. Бот обязан знать это сам, иначе будет упрямо
	// строить маршруты через ров и получать отказ от сервера.
	if w.wet(p) {
		return -1
	}
	if w.inBush(p) {
		return game.BushMoveCost
	}
	return 1
}

// field — сколько стоит дойти из клетки до ближайшей цели. Размер берётся с
// запасом под самую большую карту, как и у grid.
type field [maxDim][maxDim]int

func (f *field) at(p P) int {
	if p.X < 0 || p.X >= maxDim || p.Y < 0 || p.Y >= maxDim {
		return inf
	}
	return f[p.Y][p.X]
}

type seed struct {
	p P
	d int
}

type node struct {
	p P
	d int
}

type nodes []node

func (h nodes) Len() int           { return len(h) }
func (h nodes) Less(i, j int) bool { return h[i].d < h[j].d }
func (h nodes) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *nodes) Push(x any)        { *h = append(*h, x.(node)) }
func (h *nodes) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// occupied — надбавка за чужой отряд на клетке: свои ещё уйдут с дороги,
// а врага придётся сначала убрать.
func (w *world) occupied(p P) int {
	u := w.unitAt(p)
	switch {
	case u == nil:
		return 0
	case u.Mine:
		return 2
	case hostile(w.side, u.side):
		return 12
	}
	return 3
}

// flow — поле расстояний до целей. Считается от целей наружу, поэтому
// одного прохода хватает на все отряды этого вида. key — имя поля для кэша
// внутри шага; extra — надбавка за вход в клетку (например, под чужим
// взглядом), может быть nil.
func (w *world) flow(key string, mc mclass, seeds []seed, extra func(P) int) *field {
	key = fmt.Sprintf("%s|%v", key, mc)
	if f := w.fields[key]; f != nil {
		return f
	}
	f := &field{}
	for y := range f {
		for x := range f[y] {
			f[y][x] = inf
		}
	}
	h := &nodes{}
	for _, s := range seeds {
		if !w.in(s.p) {
			continue
		}
		if _, ok := w.enter(mc, s.p); !ok {
			continue
		}
		if s.d < f[s.p.Y][s.p.X] {
			f[s.p.Y][s.p.X] = s.d
			heap.Push(h, node{s.p, s.d})
		}
	}
	for h.Len() > 0 {
		it := heap.Pop(h).(node)
		c := it.p
		if it.d > f[c.Y][c.X] {
			continue
		}
		cost, _ := w.enter(mc, c)
		cost += w.occupied(c)
		if extra != nil {
			cost += extra(c)
		}
		for _, o := range dirs {
			n := P{X: c.X - o[0], Y: c.Y - o[1]} // шаг n → c
			if _, ok := w.enter(mc, n); !ok {
				continue
			}
			step := cost
			if o[0] != 0 && o[1] != 0 {
				if mc.wheeled {
					step *= 2
				}
				// Срезать угол между двумя непроходимыми клетками нельзя.
				_, a := w.enter(mc, P{X: n.X + o[0], Y: n.Y})
				_, b := w.enter(mc, P{X: n.X, Y: n.Y + o[1]})
				if !a && !b {
					continue
				}
			}
			if nd := it.d + step; nd < f[n.Y][n.X] {
				f[n.Y][n.X] = nd
				heap.Push(h, node{n, nd})
			}
		}
	}
	w.fields[key] = f
	return f
}

// around — клетки вокруг точки на расстоянии от lo до hi, которые есть на
// карте.
// around — клетки вокруг точки, кольцом от lo до hi. Границы — границы той
// карты, на которой идёт партия: «Теснина» и «Разделённый замок» разного
// размера, и общей мерки у них нет.
func (w *world) around(c P, lo, hi int) []P {
	var out []P
	for dy := -hi; dy <= hi; dy++ {
		for dx := -hi; dx <= hi; dx++ {
			p := c.Add(dx, dy)
			if d := max(abs(dx), abs(dy)); d >= lo && w.in(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func seeds0(ps []P) []seed {
	out := make([]seed, 0, len(ps))
	for _, p := range ps {
		out = append(out, seed{p, 0})
	}
	return out
}

// step — лучший шаг отряда к целям поля f из клеток, которые разрешил
// сервер. perStep — во сколько оценивается одна сэкономленная клетка пути,
// bonus добавляет к выбору что-то кроме дороги (удар с новой клетки,
// укрытие, опасность). Возвращает клетку и прирост оценки против того,
// чтобы стоять на месте.
func (w *world) step(u *unit, f *field, perStep float64, bonus func(P) float64) (P, float64) {
	here := f.at(u.Pos)
	// Очков действия восемь, а отрядов к середине осады два десятка: шаг
	// того, кто почти дошёл, ценнее шага того, кто только вышел из лагеря.
	// Иначе ход размазывался по всему полю и до стены не доходил никто.
	if here < inf {
		perStep *= 1 + 3/(1+float64(here)/3)
	}
	score := func(p P) float64 {
		s := 0.0
		if d := f.at(p); d < inf {
			if here >= inf {
				s = perStep * 6 // нашлась дорога, которой отсюда не было
			} else {
				s = float64(here-d) * perStep
			}
		} else if here < inf {
			return -1e9 // с дороги в тупик не сворачиваем
		}
		if bonus != nil {
			s += bonus(p)
		}
		return s
	}
	base := 0.0
	if bonus != nil {
		base = bonus(u.Pos)
	}
	best, bestS := u.Pos, base
	for _, p := range u.Moves {
		if s := score(p); s > bestS+1e-9 {
			best, bestS = p, s
		}
	}
	return best, bestS - base
}

// hasBuilding — стоит ли у стороны постройка этого вида.
func (w *world) hasBuilding(kind string, side game.Side) bool {
	for _, b := range w.v.Buildings {
		if b.Kind == kind && game.SideByName(b.Side) == side {
			return true
		}
	}
	return false
}

// moveCand — вариант шага, если он чего-то стоит.
func moveCand(u *unit, to P, score float64) []cand {
	if to == u.Pos || score <= 0 {
		return nil
	}
	return []cand{{a: game.Action{Kind: game.ActMove, UnitID: u.ID, To: to}, score: score, ap: 1}}
}

// --- оценка ударов ---

// judge — как сторона ценит цели. У осады и у замка они разные: замку
// подрывник у стены дороже рыцаря, осаде — храмовник рядом с королём.
type judge struct {
	worth func(w *world, t *unit) float64
	// wall — во что ценится урон по сегменту стены (только у осады).
	wall func(w *world, at P, dmg int) float64
}

// unitValue — чего стоит нанести цели dmg урона. Добитый отряд стоит
// дороже раненого: он больше не бьёт и не занимает клетку.
func (ev *judge) unitValue(w *world, t *unit, dmg int) float64 {
	if dmg <= 0 {
		return 0
	}
	worth := ev.worth(w, t)
	if dmg >= t.HP {
		return worth + 4
	}
	return worth * float64(dmg) / float64(max(t.MaxHP, 1))
}

// buildingValue — урон по чужой постройке. Казармы кормят замок, настил
// пускает машины к стене: их ломать выгоднее прочего.
func buildingValue(b *game.BuildingView, dmg int) float64 {
	per, kill := 0.3, 5.0
	switch b.Kind {
	case "barracks":
		per, kill = 0.5, 8
	case "outpost":
		per, kill = 0.5, 10
	case "fascine":
		per, kill = 0.4, 4
	case "bridge":
		// Сбитый мост возвращает воду под ворота и останавливает машины
		// осады: для замка это самый дорогой кусок дерева на карте.
		per, kill = 0.5, 12
	case "barricade", "palisade":
		per, kill = 0.2, 2
	}
	v := per * float64(min(dmg, b.HP))
	if dmg >= b.HP {
		v += kill
	}
	return v
}

// direct — чего стоит прямой удар отряда u с клетки from по клетке p.
func (ev *judge) direct(w *world, u *unit, from, p P) float64 {
	dist := game.Chebyshev(from, p)
	if t := w.unitAt(p); t != nil {
		if !hostile(w.side, t.side) || u.spec.NoUnits {
			return 0
		}
		if t.spec.MeleeOnly && !(dist <= 1 && u.spec.Range <= 1) {
			return 0
		}
		dmg := w.power(u)
		if t.spec.Engine {
			dmg *= max(u.spec.VsEngine, 1)
		}
		if u.Lurking && from == u.Pos {
			dmg *= game.AmbushFactor
		}
		victim := t
		if s := w.shield(t, from); s != nil {
			victim = s
		}
		return ev.unitValue(w, victim, w.cover(dmg, victim.Pos, u.spec, dist))
	}
	if w.tile(p) == game.TileWall {
		if ev.wall == nil || u.spec.VsWall <= 0 || !w.intactAt(p) {
			return 0
		}
		if seg := w.seg[p]; u.spec.GateOnly && (seg == nil || !seg.Kind.IsGate()) {
			return 0
		}
		return ev.wall(w, p, w.siege(u))
	}
	if b := w.bldAt(p); b != nil && w.targetBuilding(b) && !u.spec.GateOnly {
		return buildingValue(b, w.siege(u))
	}
	return 0
}

// area — чего стоит накрыть квадрат вокруг c. Камни не разбирают, кто
// под ними: свои отряды в накрытии стоят минуса, союзника правила не
// задевают, короля камень не берёт.
func (ev *judge) area(w *world, u *unit, c P) float64 {
	power, sieged := w.power(u), w.siege(u)
	sum := 0.0
	for dy := -u.Area; dy <= u.Area; dy++ {
		for dx := -u.Area; dx <= u.Area; dx++ {
			q := c.Add(dx, dy)
			if !w.in(q) {
				continue
			}
			dmg, hard := power, sieged
			if dx != 0 || dy != 0 {
				dmg = max(power/game.SplashEdgeDiv, game.MinRangedHit)
				hard = max(sieged/game.SplashEdgeDiv, game.MinRangedHit)
			}
			dist := game.Chebyshev(u.Pos, q)
			if t := w.unitAt(q); t != nil {
				switch {
				case t.Mine:
					hurt := w.cover(dmg, q, u.spec, dist)
					sum -= 1.5 * ownWorth(t) * float64(min(hurt, t.HP)) / float64(max(t.MaxHP, 1))
					if hurt >= t.HP {
						sum -= 10
					}
				case hostile(w.side, t.side) && !t.spec.MeleeOnly:
					sum += ev.unitValue(w, t, w.cover(dmg, q, u.spec, dist))
				}
				continue
			}
			if w.tile(q) == game.TileWall {
				if ev.wall != nil && u.spec.VsWall > 0 && w.intactAt(q) {
					sum += ev.wall(w, q, hard)
				}
				continue
			}
			if b := w.bldAt(q); b != nil && w.targetBuilding(b) {
				sum += buildingValue(b, hard)
			}
		}
	}
	return sum
}

// bestArea — лучшая клетка для навесного удара в пределах дальности.
func (ev *judge) bestArea(w *world, u *unit) (P, float64) {
	best, bestV := P{}, 0.0
	for _, c := range w.around(u.Pos, 1, reachOf(u)) {
		if v := ev.area(w, u, c); v > bestV {
			best, bestV = c, v
		}
	}
	return best, bestV
}

// bestHitFrom — лучший удар, который отряд сможет нанести, встав на p.
// Нужен, чтобы шаг к врагу оценивался ударом, который он открывает.
func (ev *judge) bestHitFrom(w *world, u *unit, p P) float64 {
	if u.Acted || u.spec.Damage <= 0 || u.spec.NoUnits || u.Area > 0 {
		return 0
	}
	r := u.spec.Range
	if u.spec.Range > 1 && w.inTower(p) {
		r += game.TowerGarrisonBonus
	}
	best := 0.0
	for _, t := range w.foes {
		d := game.Chebyshev(p, t.Pos)
		if d < 1 || d > r {
			continue
		}
		if v := ev.direct(w, u.movedTo(p), p, t.Pos); v > best {
			best = v
		}
	}
	return best
}

// movedTo — тот же отряд, но уже на клетке p. Удар с новой клетки — не
// из засады: шаг из кустов её выдаёт.
func (u *unit) movedTo(p P) *unit {
	uv := *u.UnitView
	uv.Pos, uv.Lurking = p, false
	return &unit{UnitView: &uv, typ: u.typ, spec: u.spec, side: u.side}
}

// ownWorth — во что бот ценит собственный отряд: по цене найма, с
// прибавкой за звёзды, которые не купить.
func ownWorth(u *unit) float64 {
	v := float64(u.spec.Cost) + float64(u.spec.Silver)*4
	switch {
	case u.spec.Royal:
		v = 500
	case u.typ == game.UnitTemplar:
		v = 30
	case u.typ == game.UnitMilitia:
		v = 4
	}
	return v + float64(u.Stars)*4 + 2
}

// bestStrike — лучший удар, который отряд может нанести в этом ходу: с места
// или после шага.
func (ev *judge) bestStrike(w *world, u *unit) float64 {
	if u.Acted || u.spec.Damage <= 0 || u.spec.NoUnits {
		return 0
	}
	if u.Area > 0 {
		_, v := ev.bestArea(w, u)
		return v
	}
	best := 0.0
	for _, p := range u.Targets {
		best = max(best, ev.direct(w, u, u.Pos, p))
	}
	if !u.Moved {
		for _, p := range u.Moves {
			best = max(best, ev.bestHitFrom(w, u, p))
		}
	}
	return best
}

// segAt — сегмент кладки на клетке.
func (w *world) segAt(p P) *game.WallSegment { return w.seg[p] }

// --- кладка: на «Теснине» она ряд, на второй карте кольцо ---

// wallDist — сколько шагов до ближайшего участка кладки.
func (w *world) wallDist(p P) int { return w.b.WallDistance(p) }

// openingAt — дыра ли это в обороне: пролом или открытые ворота.
func (w *world) openingAt(p P) bool {
	s := w.seg[p]
	return s != nil && (s.HP <= 0 || (s.Kind.IsGate() && s.Open))
}

// intactAt — цел ли сегмент на этой клетке.
func (w *world) intactAt(p P) bool {
	s := w.seg[p]
	return s != nil && s.HP > 0
}

// walkBehind — куда выводит подъём на стену в этом месте.
func (w *world) walkBehind(wall P) (P, bool) { return w.b.WalkBehind(wall) }

// segsByDist — участки кладки по удалению от выбранного места. Так «соседний
// столбец» превращается в «соседний сегмент», и на кольце это работает так
// же, как на прямой стене.
func (w *world) segsByDist(aim P) []P {
	out := make([]P, 0, len(w.seg))
	for p := range w.seg {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := game.Chebyshev(out[i], aim), game.Chebyshev(out[j], aim)
		if di != dj {
			return di < dj
		}
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].X < out[j].X
	})
	return out
}

// ladderTop — куда выводит лестница, стоящая на этой клетке: боевой ход за
// тем сегментом, к которому она приставлена. Снаружи, а не изнутри: изнутри
// приставлять её незачем.
func (w *world) ladderTop(p P) (P, bool) {
	if w.insideCastle(p) {
		return P{}, false
	}
	for _, d := range [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
		n := p.Add(d[0], d[1])
		if w.seg[n] == nil {
			continue
		}
		if top, ok := w.walkBehind(n); ok {
			return top, true
		}
	}
	return P{}, false
}

// sortPts — устойчивый порядок точек: бот должен ходить одинаково при
// одинаковом снимке, а обход map порядка не даёт.
func sortPts(ps []P) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Y != ps[j].Y {
			return ps[i].Y < ps[j].Y
		}
		return ps[i].X < ps[j].X
	})
}

// sortSeeds — устойчивый порядок точек старта: обход map его не даёт, а бот
// при одинаковом снимке обязан ходить одинаково.
func sortSeeds(ss []seed) {
	sort.SliceStable(ss, func(i, j int) bool {
		if ss[i].p.Y != ss[j].p.Y {
			return ss[i].p.Y < ss[j].p.Y
		}
		return ss[i].p.X < ss[j].p.X
	})
}
