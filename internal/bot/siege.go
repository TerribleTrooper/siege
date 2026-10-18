package bot

import (
	"fmt"

	"siege/internal/game"
)

// Осаждающий. Замысел у него один на осаду: выбрать столбец стены поближе
// к королю, проломить его — подрывником или катапультой — и ввести в
// пролом штурмовой отряд. Пока входа нет, отряд ждёт у сборного пункта
// вне досягаемости стены; рядом кормятся пашня и рудник.

type role uint8

const (
	roleAssault role = iota
	roleFarm
	roleMine
	roleEyes
	roleBomber
	roleSiege
	roleLadder
	roleMedic
	roleTower
	roleKing
	roleGuard
	roleShooter
	roleBlock
	roleBuilder
	roleMachine
)

// siegePlan — замысел осаждающего на этот шаг.
type siegePlan struct {
	aim      P       // участок кладки, который ломаем
	king     P       // где король: виден сейчас или середина круга поиска
	kingUnit *unit   // сам король, если виден
	search   []P     // где король может быть, пока его не видно
	entries  []P     // проломы и поднятые решётки
	ladders  []*unit // приставленные лестницы со свободным верхом
	assault  bool    // вход есть: штурмовые идут внутрь
	inside   int     // сколько своих отрядов уже в замке
	stage    P       // сборный пункт до пролома
	ready    int     // сколько штурмовых у сборного пункта или уже внутри
	farm     P
	farmFree bool
	mine     P
	mineFree bool
	ev       *judge
	roles    map[int]role
}

func (b *Bot) siege(w *world) []cand {
	pl := b.siegePlan(w)
	var cs []cand
	for _, u := range w.own {
		cs = append(cs, b.strikes(w, u, pl.ev)...)
		cs = append(cs, b.siegeWork(w, pl, u)...)
		if len(u.Moves) > 0 {
			cs = append(cs, b.siegeMove(w, pl, u)...)
		}
	}
	cs = append(cs, b.hires(w, b.siegeWants(w, pl), b.siegeReserve(w, pl))...)
	cs = append(cs, b.rally(w, pl.ev)...)
	return cs
}

func (b *Bot) siegePlan(w *world) *siegePlan {
	pl := &siegePlan{roles: map[int]role{}}

	// Король. Видим — идём к нему; не видим — осматриваем круг мест, где
	// он может быть (см. search.go).
	b.trackKing(w)
	for _, u := range w.foes {
		if u.spec.Royal {
			pl.king, pl.kingUnit = u.Pos, u
		}
	}
	if pl.kingUnit == nil {
		pl.search = b.kingMaybe()
		pl.king = middle(pl.search)
	}

	for p := range w.seg {
		if w.openingAt(p) {
			pl.entries = append(pl.entries, p)
		}
	}
	sortPts(pl.entries)
	for _, u := range append(append([]*unit{}, w.own...), w.allies...) {
		if u.typ != game.UnitLadder {
			continue
		}
		// Лестница считается приставленной, если она стоит снаружи вплотную
		// к кладке, а боевой ход за этим сегментом свободен.
		if top, ok := w.ladderTop(u.Pos); ok && w.unitAt(top) == nil {
			pl.ladders = append(pl.ladders, u)
		}
	}
	for _, u := range w.own {
		if w.insideCastle(u.Pos) || w.tile(u.Pos) == game.TileWall {
			pl.inside++
		}
	}
	pl.assault = len(pl.entries) > 0 || len(pl.ladders) > 0 || pl.inside > 0

	pl.aim = b.pickAim(w, pl.king)
	b.mem.aim = pl.aim
	pl.stage = w.b.StageTile(pl.aim, 4)
	pl.ev = &judge{worth: siegeWorth(pl), wall: wallWorth(pl)}

	// Пашня и рудник: свой фланг и тот рудник, который не занят союзником.
	farms := w.b.FarmTiles()
	pl.farm = farms[0]
	if w.side == game.SideAttackerB {
		pl.farm = farms[1]
	}
	pl.farmFree = w.free(pl.farm)
	pl.mineFree = false
	mines := w.b.MineTiles()
	for _, m := range []P{mines[1], mines[0]} { // дальний от замка безопаснее
		if u := w.unitAt(m); u != nil && u.Mine {
			pl.mine, pl.mineFree = m, true
			break
		}
		if !pl.mineFree && w.free(m) {
			pl.mine, pl.mineFree = m, true
		}
	}
	b.assignSiege(w, pl)

	for _, u := range w.own {
		if pl.roles[u.ID] != roleAssault {
			continue
		}
		if w.insideCastle(u.Pos) || game.Chebyshev(u.Pos, pl.stage) <= 3 ||
			(w.wallDist(u.Pos) <= 2 && game.Chebyshev(u.Pos, pl.aim) <= 3) {
			pl.ready++
		}
	}
	return pl
}

// free — свободна ли доходная клетка: свой на ней не мешает, чужой мешает.
func (w *world) free(p P) bool {
	u := w.unitAt(p)
	return u == nil || u.Mine
}

// pickCol выбирает, где ломать стену: поближе к королю, по своему флангу
// и, при прочих равных, там, где кладка уже подбита. Выбор держится, пока
// не появится заметно лучший: метаться вдоль стены — терять дни.
func (b *Bot) pickAim(w *world, king P) P {
	home := w.b.CampCentre(w.side)
	// Насколько далеко до участка стены на самом деле, а не по прямой: на
	// второй карте между лагерем и дальней стороной замка течёт река, и
	// ломиться туда — значит простоять всю осаду на своём берегу.
	from := []P{home}
	for _, u := range w.own {
		if !u.spec.Mechanism() {
			from = append(from, u.Pos)
		}
	}
	reach := w.flow("aim", footClass, seeds0(from), nil)
	// Считаем не до самой кладки — под ней вода рва, туда пешему хода нет, —
	// а до земли в двух-трёх шагах: оттуда и начинается штурм.
	nearCost := func(p P) int {
		best := inf
		for _, q := range w.around(p, 2, 3) {
			if d := reach.at(q); d < best {
				best = d
			}
		}
		return best
	}
	best, bestS := P{}, -1e9
	for p, s := range w.seg {
		cost := nearCost(p)
		if cost >= inf {
			continue // не дойти: за водой или за обрывом
		}
		// Ближе к королю — лучше; со своей стороны замка — тоже: иначе оба
		// осаждающих ломились бы в одни ворота, толкаясь в одном проломе.
		sc := -float64(game.Chebyshev(p, king)) - 0.45*float64(game.Chebyshev(p, home)) -
			0.35*float64(cost)
		if s.HP <= 0 {
			sc += 2 // пролом уже есть — войти можно сразу
		} else {
			sc -= 0.6 * float64(s.HP) / float64(max(s.MaxHP, 1))
		}
		if p == b.mem.aim {
			sc += 1.5 // метаться вдоль стены — терять дни
		}
		// К самой кладке надо ещё подойти: под ней вода. Там, где через ров
		// переброшен мост, подойти можно сразу — и именно туда осада
		// ломится с первого дня, а не ждёт, пока кто-нибудь набросает фашин.
		if reach.at(w.b.OutsideOf(p)) < inf {
			sc += 1.5
		}
		if sc > bestS {
			best, bestS = p, sc
		}
	}
	if bestS == -1e9 {
		// Ни до одного участка не дойти — целимся хотя бы по прямой.
		for p := range w.seg {
			if sc := -float64(game.Chebyshev(p, home)); sc > bestS {
				best, bestS = p, sc
			}
		}
	}
	return best
}

// assignSiege раздаёт роли. Кто держит пашню и рудник, помнится между
// ходами: иначе отряд, едва дойдя, тут же получал бы другую работу.
func (b *Bot) assignSiege(w *world, pl *siegePlan) {
	alive := map[int]*unit{}
	for _, u := range w.own {
		alive[u.ID] = u
	}
	if alive[b.mem.farmer] == nil {
		b.mem.farmer = 0
	}
	if alive[b.mem.miner] == nil {
		b.mem.miner = 0
	}
	if b.prof.economy {
		if b.mem.farmer == 0 && pl.farmFree && w.v.Day <= game.SiegeDays-15 {
			b.mem.farmer = b.closest(w, pl.farm, func(u *unit) bool {
				return (u.typ == game.UnitScout || u.typ == game.UnitInfantry) && u.ID != b.mem.miner
			})
		}
		if b.mem.miner == 0 && pl.mineFree && w.v.Day <= game.SiegeDays-10 {
			b.mem.miner = b.closest(w, pl.mine, func(u *unit) bool {
				return (u.typ == game.UnitInfantry || u.typ == game.UnitScout) && u.ID != b.mem.farmer
			})
		}
	}
	for _, u := range w.own {
		r := roleAssault
		switch {
		case u.ID == b.mem.farmer:
			r = roleFarm
		case u.ID == b.mem.miner:
			r = roleMine
		case u.typ == game.UnitBomber:
			r = roleBomber
		case u.typ == game.UnitCatapult:
			r = roleSiege
		case u.typ == game.UnitLadder:
			r = roleLadder
		case u.typ == game.UnitHealer:
			r = roleMedic
		case u.typ == game.UnitScout:
			r = roleEyes
		case u.typ == game.UnitSiegeTower:
			r = roleTower
		}
		pl.roles[u.ID] = r
	}
}

// closest — ближайший к точке свой отряд, подходящий под условие.
func (b *Bot) closest(w *world, to P, ok func(*unit) bool) int {
	best, bestD := 0, inf
	for _, u := range w.own {
		if !ok(u) {
			continue
		}
		if d := game.Chebyshev(u.Pos, to); d < bestD {
			best, bestD = u.ID, d
		}
	}
	return best
}

// siegeWorth — во что осада ценит отряды замка. Король дороже всего, а
// заслон рядом с ним дороже такого же в поле: снять его — открыть дорогу.
func siegeWorth(pl *siegePlan) func(w *world, t *unit) float64 {
	return func(w *world, t *unit) float64 {
		v := 8.0
		switch t.typ {
		case game.UnitKing:
			return 300
		case game.UnitTemplar:
			v = 12
		case game.UnitGarrison:
			v = 10
		case game.UnitMilitia:
			v = 6
		case game.UnitEngineer:
			v = 9
		case game.UnitCrossbow:
			v = 15
		case game.UnitKnight:
			v = 18
		case game.UnitBallista, game.UnitTrebuchet:
			v = 20
		case game.UnitHealer:
			v = 7
		}
		v += 2 * float64(t.Stars)
		if pl.kingUnit != nil && game.Chebyshev(t.Pos, pl.king) <= 1 {
			v *= 1.5
		}
		return v
	}
}

// wallWorth — урон по кладке. Свой столбец и соседние ценятся выше, а
// удар, который роняет сегмент, — отдельно: пролом и есть цель.
func wallWorth(pl *siegePlan) func(w *world, at P, dmg int) float64 {
	return func(w *world, at P, dmg int) float64 {
		s := w.seg[at]
		if s == nil || s.HP <= 0 {
			return 0
		}
		near := game.Chebyshev(at, pl.aim)
		per := 0.25
		switch near {
		case 0:
			per = 0.9
		case 1:
			per = 0.5
		}
		v := per * float64(min(dmg, s.HP))
		if dmg >= s.HP {
			if near <= 1 {
				v += 20
			} else {
				v += 6
			}
		}
		return v
	}
}

// --- шаги ---

func (b *Bot) siegeMove(w *world, pl *siegePlan, u *unit) []cand {
	r := pl.roles[u.ID]
	hit := func(p P) float64 { return 0.85 * pl.ev.bestHitFrom(w, u, p) }
	inside := w.insideCastle(u.Pos) || w.tile(u.Pos) == game.TileWall

	switch {
	case r == roleFarm || r == roleMine:
		goal := pl.farm
		if r == roleMine {
			goal = pl.mine
		}
		if u.Pos == goal {
			return nil
		}
		f := w.flow(fmt.Sprintf("goal@%v", goal), classOf(u.spec), seeds0([]P{goal}), nil)
		to, s := w.step(u, f, 3, b.risk(w, u, 0.5))
		return moveCand(u, to, s)

	case r == roleBomber:
		return b.bomberMove(w, pl, u)

	case r == roleSiege:
		return b.catapultMove(w, pl, u)

	case r == roleLadder:
		return b.ladderMove(w, pl, u)

	case r == roleTower || u.spec.Move == 0:
		return nil

	case r == roleMedic:
		f := w.flow(fmt.Sprintf("medic#%d", u.ID), classOf(u.spec), b.woundedSeeds(w, u, pl), nil)
		to, s := w.step(u, f, 1, b.risk(w, u, 0.8))
		return moveCand(u, to, s)

	case r == roleEyes && !inside:
		var f *field
		if pl.assault {
			f = w.flow("eyes-in", footClass, b.entrySeeds(w, pl), nil)
		} else {
			f = w.flow("eyes", footClass, b.lookoutSeeds(w, pl), nil)
		}
		risk := b.risk(w, u, 0.6)
		to, s := w.step(u, f, 1.1, func(p P) float64 { return hit(p) - risk(p) })
		return moveCand(u, to, s)
	}

	// Штурмовые и все, кто уже внутри.
	if pl.assault || inside {
		f := b.kingField(w, pl)
		risk := b.risk(w, u, 0.25)
		to, s := w.step(u, f, 2, func(p P) float64 { return hit(p) - risk(p) })
		return moveCand(u, to, s)
	}
	f := w.flow("stage", footClass, b.stageSeeds(w, pl), nil)
	risk := b.risk(w, u, 0.4)
	to, s := w.step(u, f, 1, func(p P) float64 { return hit(p) - risk(p) })
	return moveCand(u, to, s)
}

// risk — во что обойдётся встать на клетку под чужие удары. Засевший в
// кустах незамеченным не цель вовсе: по нему не прицелиться. weight — как
// дорожит отрядом бот: осторожный бережёт, лёгкий не смотрит.
func (b *Bot) risk(w *world, u *unit, weight float64) func(P) float64 {
	if !b.prof.cautious {
		weight *= 0.3
	}
	hidden := u.Lurking || !w.watched(u.Pos)
	return func(p P) float64 {
		if w.inBush(p) && hidden {
			return 0
		}
		t := w.threat(p, u)
		if t <= 0 {
			return 0
		}
		frac := float64(min(t, u.HP)) / float64(max(u.HP, 1))
		if t >= u.HP {
			frac += 0.5 // не ранят, а убьют
		}
		return weight * frac * ownWorth(u)
	}
}

// kingField — дорога к королю: на соседние с ним клетки, через проломы и
// по приставленным лестницам. Пока короля не видно — к местам, где он может
// быть: каждый отряд идёт к ближайшему, и вместе они прочёсывают двор.
func (b *Bot) kingField(w *world, pl *siegePlan) *field {
	var goals []seed
	if pl.kingUnit != nil {
		for _, o := range dirs {
			if p := pl.king.Add(o[0], o[1]); w.insideCastle(p) {
				goals = append(goals, seed{p, 0})
			}
		}
	} else {
		goals = b.searchSeeds(w, pl.search)
	}
	f := w.flow("king", footClass, goals, nil)
	if len(pl.ladders) == 0 {
		return f
	}
	// Лестница — дверь в стене: из соседней с ней клетки на боевой ход
	// попадают одним действием.
	all := append([]seed{}, goals...)
	for _, l := range pl.ladders {
		top, ok := w.ladderTop(l.Pos)
		if !ok {
			continue
		}
		d := f.at(top)
		if d >= inf {
			continue
		}
		for _, o := range dirs {
			all = append(all, seed{l.Pos.Add(o[0], o[1]), d + 2})
		}
	}
	return w.flow("king+ladder", footClass, all, nil)
}

// stageSeeds — сборный пункт: клетки у выбранного столбца, на два ряда
// дальше досягаемости стрелков со стены. Кусты в нём лучше поля.
func (b *Bot) stageSeeds(w *world, pl *siegePlan) []seed {
	var out []seed
	for _, p := range w.around(pl.stage, 0, 2) {
		if w.wallDist(p) <= 2 || !w.in(p) {
			continue
		}
		d := 1
		if w.inBush(p) {
			d = 0
		}
		out = append(out, seed{p, d})
	}
	return out
}

// lookoutSeeds — где разведчику стоять до пролома: в кустах перед стеной у
// выбранного столбца. Оттуда видно боевой ход, а самого его не видно.
func (b *Bot) lookoutSeeds(w *world, pl *siegePlan) []seed {
	var out []seed
	for _, p := range w.around(pl.aim, 2, 6) {
		d := w.wallDist(p)
		if !w.in(p) || d < 2 || d > 5 || w.insideCastle(p) {
			continue
		}
		switch {
		case w.inBush(p):
			out = append(out, seed{p, 0})
		case d >= 3 && game.Chebyshev(p, pl.aim) <= 4:
			out = append(out, seed{p, 3})
		}
	}
	return out
}

// entrySeeds — у входа в замок: разведчик в проломе видит весь двор.
func (b *Bot) entrySeeds(w *world, pl *siegePlan) []seed {
	var out []seed
	for _, e := range pl.entries {
		out = append(out, seed{e, 0})
		if top, ok := w.walkBehind(e); ok {
			out = append(out, seed{top, 0})
		}
	}
	for _, l := range pl.ladders {
		for _, o := range dirs {
			out = append(out, seed{l.Pos.Add(o[0], o[1]), 1})
		}
	}
	if len(out) == 0 {
		return b.lookoutSeeds(w, pl)
	}
	return out
}

// woundedSeeds — к кому идти лекарю: к самому израненному своему отряду, а
// если таких нет — за штурмовыми.
func (b *Bot) woundedSeeds(w *world, healer *unit, pl *siegePlan) []seed {
	var worst *unit
	for _, u := range w.own {
		if u == healer || u.spec.Mechanism() || u.MaxHP-u.HP < game.HealAmount-1 {
			continue
		}
		if worst == nil || u.MaxHP-u.HP > worst.MaxHP-worst.HP {
			worst = u
		}
	}
	if worst == nil {
		return b.stageSeeds(w, pl)
	}
	var out []seed
	for _, o := range dirs {
		out = append(out, seed{worst.Pos.Add(o[0], o[1]), 0})
	}
	return out
}

// bomberMove ведёт подрывника под стену у выбранного столбца. Прочности у
// него единица, поэтому осторожный бот ждёт, пока соберётся штурмовой
// отряд, и подходит кустами.
func (b *Bot) bomberMove(w *world, pl *siegePlan, u *unit) []cand {
	target, has := b.chargeAt(w, pl)
	wait := !has || (b.prof.gather > 0 && pl.ready < b.prof.gather)
	var f *field
	if wait {
		f = w.flow("bomber-wait", footClass, b.stageSeeds(w, pl), nil)
	} else {
		// Подходим к самому сегменту и к соседним: заряд кладут вплотную.
		var goals []P
		for _, p := range w.segsByDist(target) {
			if game.Chebyshev(p, target) > 1 {
				break
			}
			goals = append(goals, w.b.OutsideOf(p))
		}
		var extra func(P) int
		if b.prof.cautious {
			// Под чужим взглядом каждая клетка дороже: лучше крюк по
			// кустам и туману, чем болт в первый же день у стены.
			extra = func(p P) int {
				if w.watched(p) && !w.inBush(p) {
					return 3
				}
				return 0
			}
		}
		f = w.flow(fmt.Sprintf("bomber#%d,%d", target.X, target.Y), footClass, seeds0(goals), extra)
	}
	risk := b.risk(w, u, 1.5)
	to, s := w.step(u, f, 2.5, func(p P) float64 {
		// Дошёл до стены — тут же и взорвал: ответного хода у замка не будет.
		if !wait && w.wallDist(p) == 1 && game.Chebyshev(p, target) <= 1 && !w.insideCastle(p) {
			return 0
		}
		return -risk(p)
	})
	return moveCand(u, to, s)
}

// chargeCol — под какой сегмент вести подрывника: выбранный, а если он уже
// лежит — ближайший к нему целый, чтобы пролом стал шире. 0 — некуда.
func (b *Bot) chargeAt(w *world, pl *siegePlan) (P, bool) {
	for _, p := range w.segsByDist(pl.aim) {
		if game.Chebyshev(p, pl.aim) > 3 {
			break
		}
		if w.intactAt(p) {
			return p, true
		}
	}
	return P{}, false
}

// catapultMove ставит катапульту на дальность выстрела по выбранному
// столбцу — как можно дальше от стены: ближе подходить незачем, а
// стрелки на боевом ходу бьют недалеко.
func (b *Bot) catapultMove(w *world, pl *siegePlan, u *unit) []cand {
	aim := pl.aim
	r := u.spec.Range
	var goals []seed
	for _, p := range w.around(aim, 4, r) {
		if !w.in(p) || w.insideCastle(p) || w.tile(p) == game.TileWall {
			continue
		}
		goals = append(goals, seed{p, r - game.Chebyshev(p, aim)})
	}
	f := w.flow("catapult", classOf(u.spec), goals, nil)
	shot := func(p P) float64 {
		if u.Acted || game.Chebyshev(p, aim) > r {
			return 0
		}
		return 0.8 * pl.ev.area(w, u.movedTo(p), aim)
	}
	risk := b.risk(w, u, 1)
	to, s := w.step(u, f, 1.2, func(p P) float64 { return shot(p) - risk(p) })
	return moveCand(u, to, s)
}

// ladderMove приставляет лестницу к стене рядом с выбранным столбцом — но
// не к нему самому: туда идёт подрывник, и второй вход полезнее.
func (b *Bot) ladderMove(w *world, pl *siegePlan, u *unit) []cand {
	if _, ok := w.ladderTop(u.Pos); ok {
		return nil // уже у стены
	}
	// Второй вход полезнее третьего удара в те же ворота: отходим на пару
	// сегментов вдоль кладки от того, который ломает подрывник.
	near := w.segsByDist(pl.aim)
	spot := pl.aim
	if len(near) > 2 {
		spot = near[2]
	}
	f := w.flow("ladder", classOf(u.spec), seeds0([]P{w.b.OutsideOf(spot)}), nil)
	to, s := w.step(u, f, 1.5, b.risk(w, u, 0.6))
	return moveCand(u, to, s)
}

// --- работа: заряд, лестница, фашины, лечение, башня ---

func (b *Bot) siegeWork(w *world, pl *siegePlan, u *unit) []cand {
	var out []cand
	add := func(a game.Action, score float64, ap int) {
		a.UnitID = u.ID
		out = append(out, cand{a: a, score: score, ap: ap})
	}
	for _, p := range u.ChargeAt {
		add(game.Action{Kind: game.ActCharge, To: p}, max(70-5*float64(game.Chebyshev(p, pl.aim)), 25), 1)
	}
	// Аванпост у сборного пункта: дальше отряды нанимаются прямо у стены, а
	// не бредут по четыре дня из лагеря. Очков действия на это уходит два,
	// а экономит он их десятки.
	if b.prof.outpost && len(u.OutpostAt) > 0 && w.v.You.Supplies >= game.OutpostCost &&
		!w.hasBuilding("outpost", w.side) && w.v.Day <= game.SiegeDays-game.OutpostLife/2 {
		for _, p := range u.OutpostAt {
			d := game.Chebyshev(p, pl.stage)
			if d > 3 || w.threat(p, nil) > 0 && b.prof.cautious {
				continue
			}
			add(game.Action{Kind: game.ActBuild, Build: "outpost", To: p}, 16-2*float64(d), game.OutpostAPCost)
		}
	}
	if u.CanClimb {
		add(game.Action{Kind: game.ActClimb}, 14, 1)
	}
	if b.prof.fascines && u.typ == game.UnitInfantry {
		for _, p := range u.FascineAt {
			if game.Chebyshev(p, pl.aim) <= 1 || w.openingAt(p) {
				add(game.Action{Kind: game.ActBuild, Build: "fascine", To: p}, 2.2, u.FascineAP)
				break
			}
		}
	}
	for _, p := range u.HealAt {
		if t := w.unitAt(p); t != nil {
			gain := min(game.HealAmount, t.MaxHP-t.HP)
			add(game.Action{Kind: game.ActHeal, To: p}, 1.2*ownWorth(t)*float64(gain)/float64(max(t.MaxHP, 1)), 1)
		}
	}
	// Осадная башня бота сама не строится, но доставшуюся от человека бот
	// разгружает: людям внутри лучше на стене или хотя бы на земле.
	if len(u.Cargo) > 0 {
		switch {
		case u.Ramp && len(u.UnloadAt) > 0:
			out = append(out, cand{a: game.Action{Kind: game.ActUnload, UnitID: u.Cargo[0].ID, To: u.UnloadAt[0]}, score: 15, ap: 1})
		case u.CanRamp:
			add(game.Action{Kind: game.ActRamp}, 14, 1)
		case len(u.ExitAt) > 0:
			out = append(out, cand{a: game.Action{Kind: game.ActUnload, UnitID: u.Cargo[0].ID, To: u.ExitAt[0]}, score: 4, ap: 1})
		}
	}
	return out
}

// --- наём ---

func (b *Bot) siegeWants(w *world, pl *siegePlan) []want {
	var out []want
	day := w.v.Day
	count := map[role]int{}
	for _, u := range w.own {
		count[pl.roles[u.ID]]++
	}
	stage := pl.stage
	if b.prof.economy {
		if b.mem.farmer == 0 && pl.farmFree && day <= game.SiegeDays-15 {
			out = append(out, want{t: game.UnitScout, prio: 9, near: pl.farm})
		}
		if b.mem.miner == 0 && pl.mineFree && day <= game.SiegeDays-10 {
			out = append(out, want{t: game.UnitInfantry, prio: 7.5, near: pl.mine})
		}
	}
	if b.prof.elite && w.v.You.Silver >= game.Spec(game.UnitKnight).Silver {
		out = append(out, want{t: game.UnitKnight, prio: 9, near: stage})
	}
	hasCharge := false
	if _, ok := b.chargeAt(w, pl); ok {
		hasCharge = true
	}
	if b.prof.bombers > 0 && w.count(game.UnitBomber) < b.prof.bombers && day >= 2 && hasCharge {
		out = append(out, want{t: game.UnitBomber, prio: 8, near: stage})
	}
	if count[roleEyes] < b.prof.eyes {
		out = append(out, want{t: game.UnitScout, prio: 6, near: stage})
	}
	if w.count(game.UnitCatapult) < b.prof.catapults && day >= 3 {
		out = append(out, want{t: game.UnitCatapult, prio: 6.5, near: stage})
	}
	if w.count(game.UnitLadder) < b.prof.ladders && pl.ready >= 2 {
		out = append(out, want{t: game.UnitLadder, prio: 6, near: stage})
	}
	// С аванпостом наём выходит прямо к стене, и лишние припасы — а сверх
	// потолка они всё равно пропадут — уходят в штурмовых. Без него новый
	// отряд четыре дня идёт от лагеря, и толпа в поле только ест очки.
	//
	// Когда бой уже идёт внутри замка, исход решают те, кто там: очки
	// действия нужны им — найти короля и ударить. Наём тогда идёт только на
	// остаток очков, иначе половина хода уходила на пехоту у аванпоста, а
	// отрядам в замке не хватало шагов.
	assault := b.prof.assault
	if s := w.v.You.Supplies; s > 40 && w.hasBuilding("outpost", w.side) {
		assault += (s - 40) / 6
	}
	if count[roleAssault] < assault {
		prio := 5.0
		switch {
		case pl.inside > 0:
			prio = 2
		case pl.assault:
			prio = 6.5
		}
		out = append(out, want{t: game.UnitInfantry, prio: prio, near: stage})
	}
	if b.prof.healer && w.count(game.UnitHealer) == 0 && count[roleAssault] >= 5 {
		out = append(out, want{t: game.UnitHealer, prio: 4.5, near: stage})
	}
	// Арбалетчики — на остаток серебра: рыцарь штурму нужнее, но копить
	// серебро без конца тоже незачем.
	if !b.prof.elite && w.v.You.Silver >= game.Spec(game.UnitCrossbow).Silver {
		out = append(out, want{t: game.UnitCrossbow, prio: 5, near: stage})
	}
	return out
}

// siegeReserve — сколько припасов не трогать на наём. Воодушевление стоит
// всего обоза, и осторожный бот, когда штурм уже идёт, копит на него, а не
// тратит последнее на очередного пехотинца.
func (b *Bot) siegeReserve(w *world, pl *siegePlan) int {
	if b.prof.outpost && !w.hasBuilding("outpost", w.side) && pl.ready >= 2 {
		return game.OutpostCost
	}
	if !b.prof.rally || b.prof.gather == 0 || pl.kingUnit == nil {
		return 0
	}
	for _, u := range w.own {
		if game.Chebyshev(u.Pos, pl.king) <= 3 {
			return game.RallyCost
		}
	}
	return 0
}

// deploySpot — где выставить новый отряд: ближайшая к нужному месту клетка
// своей зоны найма.
func (w *world) deploySpot(wt want) (P, bool) {
	spec := game.Spec(wt.t)
	best, bestD, ok := P{}, 1e9, false
	for _, p := range w.v.You.Zone {
		if spec.CampOnly && w.b.CampSide(p) != w.side || wt.walk && w.tile(p) != game.TileWalk {
			continue
		}
		d := float64(game.Chebyshev(p, wt.near)) + 0.3*float64(abs(p.Y-wt.near.Y))
		if d < bestD {
			best, bestD, ok = p, d, true
		}
	}
	return best, ok
}
