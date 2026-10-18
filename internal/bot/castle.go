package bot

import (
	"fmt"

	"siege/internal/game"
)

// Замок. Его дело — продержаться семьдесят дней: держать стрелков на
// боевом ходу и бить всё, что подходит к стене (подрывника — первым),
// закрывать проломы, а короля уводить от них подальше, под охрану
// храмовников. Казармы во дворе — это доход на всю осаду, их ставят сразу.

// castlePlan — замысел защитника на этот шаг.
type castlePlan struct {
	king    *unit
	threats []P     // откуда войдут: проломы, решётки, верх лестниц, враги внутри
	inside  []*unit // враги в замке и в проломах
	alarm   bool    // враг уже внутри
	front   P       // участок кладки, у которого враг жмётся сильнее всего
	safety  *field  // далеко ли клетки замка от ближайшей угрозы
	ev      *judge
	roles   map[int]role
	mend    []seed // куда идти инженерам: к пролому или подбитой кладке
}

func (b *Bot) defend(w *world) []cand {
	pl := b.castlePlan(w)
	var cs []cand
	for _, u := range w.own {
		cs = append(cs, b.strikes(w, u, pl.ev)...)
		cs = append(cs, b.castleWork(w, pl, u)...)
		if len(u.Moves) > 0 {
			cs = append(cs, b.castleMove(w, pl, u)...)
		}
	}
	cs = append(cs, b.castleSkills(w, pl)...)
	cs = append(cs, b.rally(w, pl.ev)...)
	cs = append(cs, b.hires(w, b.castleWants(w, pl), b.castleReserve(w, pl))...)
	cs = append(cs, b.castleBuilds(w, pl)...)
	return cs
}

func (b *Bot) castlePlan(w *world) *castlePlan {
	pl := &castlePlan{roles: map[int]role{}, ev: &judge{worth: castleWorth}}
	for _, u := range w.own {
		if u.spec.Royal {
			pl.king = u
		}
	}
	for p := range w.seg {
		if w.openingAt(p) {
			pl.threats = append(pl.threats, p)
		}
	}
	for _, e := range w.foes {
		switch {
		case w.insideCastle(e.Pos) || w.tile(e.Pos) == game.TileWall:
			pl.inside = append(pl.inside, e)
			pl.threats = append(pl.threats, e.Pos)
		case e.typ == game.UnitLadder || e.typ == game.UnitSiegeTower:
			// Приставленная лестница и подведённая башня — это завтрашний
			// отряд на боевом ходу: встречаем его там, где он поднимется.
			if top, ok := w.ladderTop(e.Pos); ok {
				pl.threats = append(pl.threats, top)
				for _, o := range dirs {
					if n := top.Add(o[0], o[1]); w.tile(n) == game.TileWalk {
						pl.threats = append(pl.threats, n)
					}
				}
			}
		case e.typ == game.UnitBomber && w.wallDist(e.Pos) <= 3:
			// Подрывник у стены — это пролом уже завтра: король уходит
			// заранее, а не когда в проломе покажутся мечи.
			for _, p := range w.segsByDist(e.Pos) {
				if game.Chebyshev(p, e.Pos) > 2 {
					break
				}
				if !w.intactAt(p) {
					continue
				}
				if top, ok := w.walkBehind(p); ok {
					pl.threats = append(pl.threats, top)
				}
			}
		}
	}
	sortPts(pl.threats)
	pl.alarm = len(pl.inside) > 0
	pl.front = b.front(w)
	if len(pl.threats) > 0 {
		pl.safety = w.flow("safety", footClass, seeds0(pl.threats), nil)
	}
	pl.mend = b.mendSeeds(w)
	b.assignCastle(w, pl)
	return pl
}

// front — участок кладки, где враг ближе всего: туда тянутся стрелки.
// Ближние к стене весят больше дальних, а на кольце «столбца» нет — есть
// сегмент, к которому они жмутся.
func (b *Bot) front(w *world) P {
	// Где давят сильнее всего: каждый враг снаружи тянет к себе ближние
	// сегменты, и чем он ближе к стене, тем сильнее тянет. Выигрывает не
	// середина между двумя осаждающими, а тот участок, у которого их
	// больше: середина — это место, где нет никого.
	best, bestS := P{}, 0.0
	for p := range w.seg {
		score := 0.0
		for _, e := range w.foes {
			if w.insideCastle(e.Pos) {
				continue
			}
			wt := 1 / float64(max(w.wallDist(e.Pos), 1))
			score += wt / float64(1+game.Chebyshev(p, e.Pos))
		}
		if score > bestS {
			best, bestS = p, score
		}
	}
	if bestS == 0 {
		if b.mem.front != (P{}) {
			return b.mem.front
		}
		// Никого не видно — стоим у ворот: их ломают первыми.
		if gates := w.b.GateTiles(); len(gates) > 0 {
			return gates[0]
		}
		return w.segsByDist(w.b.ThroneTile())[0]
	}
	b.mem.front = best
	return best
}

func (b *Bot) assignCastle(w *world, pl *castlePlan) {
	mine := P{}
	if ms := w.b.CastleMines(); len(ms) > 0 {
		mine = ms[0]
	}
	alive := map[int]bool{}
	for _, u := range w.own {
		alive[u.ID] = true
	}
	if !alive[b.mem.miner] {
		b.mem.miner = 0
	}
	if u := w.unitAt(mine); u != nil && u.Mine {
		b.mem.miner = u.ID
	}
	if b.prof.castleMine && b.mem.miner == 0 {
		// На рудник — ближний разведчик со двора: с угловой клетки он всё
		// равно никого не достанет, а серебро капает.
		for _, u := range w.own {
			if u.typ == game.UnitScout && game.Chebyshev(u.Pos, mine) <= 2 {
				b.mem.miner = u.ID
			}
		}
	}
	for _, u := range w.own {
		r := roleBlock
		switch {
		case u.ID == b.mem.miner:
			r = roleMine
		case u.spec.Royal:
			r = roleKing
		case u.typ == game.UnitTemplar:
			r = roleGuard
		case u.spec.Move == 0:
			r = roleMachine
		case u.spec.Builder:
			r = roleBuilder
		case u.spec.Healer:
			r = roleMedic
		case u.spec.Range > 1:
			r = roleShooter
		}
		pl.roles[u.ID] = r
	}
}

// castleWorth — во что замок ценит отряды осады. Подрывник у стены дороже
// всего: дойдёт — и сегмента нет. Всё, что уже внутри, вдвое дороже.
func castleWorth(w *world, t *unit) float64 {
	v := 8.0
	switch t.typ {
	case game.UnitBomber:
		v = 40
		if w.wallDist(t.Pos) <= 4 {
			v += 25
		}
	case game.UnitCatapult, game.UnitRam:
		v = 26
	case game.UnitSiegeTower:
		v = 30
	case game.UnitLadder:
		v = 8
		if w.wallDist(t.Pos) <= 1 {
			v += 20
		}
	case game.UnitKnight:
		v = 18
	case game.UnitCrossbow:
		v = 14
	case game.UnitInfantry:
		v = 9
	case game.UnitScout:
		v = 7
	case game.UnitHealer:
		v = 7
	case game.UnitMantlet:
		v = 4
	}
	v += 2 * float64(t.Stars)
	switch {
	case w.insideCastle(t.Pos) || w.tile(t.Pos) == game.TileWall:
		v *= 2
	case w.wallDist(t.Pos) <= 1:
		v *= 1.3
	}
	return v
}

// mendSeeds — к чему идти инженерам: к пролому, который можно заколотить
// (на месте замковых ворот деревянные не ставят), и к сильно подбитой
// кладке. Стоят они при этом на боевом ходу рядом.
func (b *Bot) mendSeeds(w *world) []seed {
	if !b.prof.mend {
		return nil
	}
	var out []seed
	for p, s := range w.seg {
		d := -1
		switch {
		case s.HP <= 0 && s.Kind != game.SegGate && w.unitAt(p) == nil:
			d = 0
		case s.HP > 0 && s.HP*10 <= s.MaxHP*7:
			d = 2
		}
		if d < 0 {
			continue
		}
		if top, ok := w.walkBehind(p); ok {
			out = append(out, seed{top, d})
			for _, o := range dirs {
				if n := top.Add(o[0], o[1]); w.tile(n) == game.TileWalk {
					out = append(out, seed{n, d})
				}
			}
		}
	}
	sortSeeds(out)
	return out
}

// --- шаги ---

func (b *Bot) castleMove(w *world, pl *castlePlan, u *unit) []cand {
	hit := func(p P) float64 { return 0.85 * pl.ev.bestHitFrom(w, u, p) }
	var seeds []seed
	key, per := "", 1.0
	switch pl.roles[u.ID] {
	case roleKing:
		return b.kingMove(w, pl, u)
	case roleMachine:
		return nil
	case roleMine:
		key, per = "mine", 3
		seeds = seeds0(w.b.CastleMines())
	case roleGuard:
		key, per = "guard", 2
		seeds = kingRing(w, pl)
	case roleBuilder:
		if len(pl.mend) == 0 {
			return nil
		}
		key, per, seeds = "mend", 1.5, pl.mend
	case roleMedic:
		key, per = fmt.Sprintf("medic#%d", u.ID), 1
		seeds = woundedRing(w, u)
	case roleShooter:
		key, per = "post", 0.5
		for p := range w.seg {
			top, ok := w.walkBehind(p)
			if !ok {
				continue
			}
			seeds = append(seeds, seed{top, game.Chebyshev(p, pl.front) / 2})
		}
		sortSeeds(seeds)
	default: // ближний бой
		key, per, seeds = b.blockSeeds(w, pl)
	}
	if len(seeds) == 0 {
		return nil
	}
	f := w.flow(key, classOf(u.spec), seeds, nil)
	to, s := w.step(u, f, per, hit)
	return moveCand(u, to, s)
}

// kingRing — клетки вокруг короля: там место храмовников.
func kingRing(w *world, pl *castlePlan) []seed {
	if pl.king == nil {
		return nil
	}
	var out []seed
	for _, o := range dirs {
		if p := pl.king.Pos.Add(o[0], o[1]); w.insideCastle(p) {
			out = append(out, seed{p, 0})
		}
	}
	return out
}

// woundedRing — к самому израненному своему отряду.
func woundedRing(w *world, healer *unit) []seed {
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
		return nil
	}
	var out []seed
	for _, o := range dirs {
		out = append(out, seed{worst.Pos.Add(o[0], o[1]), 0})
	}
	return out
}

// blockSeeds — куда идти бойцам ближнего боя. Враг внутри — к нему, и в
// первую очередь к тому, кто ближе к королю. Пролом или лестница — встать
// в проём или на верх лестницы: занятая клетка не пускает внутрь. Иначе —
// ждать во дворе за участком, где враг ближе всего.
func (b *Bot) blockSeeds(w *world, pl *castlePlan) (string, float64, []seed) {
	if pl.alarm {
		var out []seed
		for _, e := range pl.inside {
			d := 0
			if pl.king != nil {
				d = game.Chebyshev(e.Pos, pl.king.Pos) / 2
			}
			for _, o := range dirs {
				out = append(out, seed{e.Pos.Add(o[0], o[1]), d})
			}
		}
		return "fight", 2.5, out
	}
	var out []seed
	for _, t := range pl.threats {
		out = append(out, seed{t, 0})
		if top, ok := w.walkBehind(t); ok {
			out = append(out, seed{top, 1})
		}
	}
	if len(out) > 0 {
		return "plug", 2, out
	}
	// Тихо — ждём во дворе, ближе к тому участку, где давят.
	for _, p := range w.b.InsideTiles() {
		if w.tile(p) != game.TileCourtyard || w.wallDist(p) != 2 {
			continue
		}
		out = append(out, seed{p, game.Chebyshev(p, pl.front)})
	}
	sortSeeds(out)
	return "wait", 0.4, out
}

// kingMove уводит короля от угроз. Новобранец двигает его, только когда
// враг уже вплотную; остальные — заранее, едва появился пролом, и не на
// клетку в сторону, а в убежище: в дальний от угроз угол двора. Жадный шаг
// тут не работает — вдоль широкого пролома все соседние клетки одинаково
// близки к нему, и король стоял бы, пока к нему не подойдут.
func (b *Bot) kingMove(w *world, pl *castlePlan, u *unit) []cand {
	near := func(p P) int {
		n := 0
		for _, e := range w.foes {
			if e.spec.Damage > 0 && e.spec.Range <= 1 && game.Chebyshev(e.Pos, p) <= 1 {
				n++
			}
		}
		return n
	}
	guarded := func(p P) float64 {
		s := 0.0
		for _, g := range w.own {
			if g.typ == game.UnitTemplar && game.Chebyshev(g.Pos, p) <= 1 {
				s += 0.5 // под охраной стражи стоять лучше
			}
		}
		return s
	}
	if !b.prof.kingFlee {
		if near(u.Pos) == 0 {
			return nil
		}
		eval := func(p P) float64 {
			d := inf
			for _, e := range pl.inside {
				d = min(d, game.Chebyshev(e.Pos, p))
			}
			return float64(min(d, 20)) - 30*float64(near(p))
		}
		best, bestV := u.Pos, eval(u.Pos)
		for _, p := range u.Moves {
			if v := eval(p); v > bestV+1e-9 {
				best, bestV = p, v
			}
		}
		return moveCand(u, best, (bestV-eval(u.Pos))*3)
	}
	// В убежище король уходит заранее, ещё до пролома: в спокойные дни шаг
	// короля ничего не стоит, а когда пролом уже есть, путь из середины
	// двора в угол лежит как раз мимо него — и обступившие трон свои же
	// отряды не дают шагнуть.
	cur := inf
	if pl.safety != nil {
		cur = pl.safety.at(u.Pos)
	}
	home := b.refuge(w, pl, u)
	fk := w.flow("refuge", classOf(u.spec), seeds0([]P{home}), nil)
	if fk.at(u.Pos) == 0 && near(u.Pos) == 0 {
		return nil // уже в убежище
	}
	safe := func(p P) float64 {
		if pl.safety == nil {
			return 0
		}
		return 0.3 * float64(min(pl.safety.at(p), 30))
	}
	eval := func(p P) float64 {
		d := fk.at(p)
		if d >= inf {
			d = 40
		}
		return safe(p) - float64(d) - 30*float64(near(p)) + guarded(p)
	}
	best, bestV := u.Pos, eval(u.Pos)
	for _, p := range u.Moves {
		if v := eval(p); v > bestV+1e-9 {
			best, bestV = p, v
		}
	}
	gain := bestV - eval(u.Pos)
	if gain <= 0 {
		return nil
	}
	score := 1.5 * gain
	// Пролом рядом — увести короля важнее всего остального: найм и стройка
	// подождут, а потом из-за спин собственной стражи и ополчения шагнуть
	// может быть уже некуда.
	if cur <= 10 || near(u.Pos) > 0 {
		score += 18
	}
	return moveCand(u, best, score)
}

// refuge — куда прятать короля. Лучший угол двора — тот, что дальше от
// угроз (пока их нет — от участка стены, к которому жмётся враг), до
// которого король дойдёт и который со всех сторон уже прикрыт стенами и
// постройками: там короля обступит малая стража. Выбор держится, пока
// прежний угол не стал заметно хуже: король, мечущийся между двумя углами
// двора, топчется у трона.
func (b *Bot) refuge(w *world, pl *castlePlan, king *unit) P {
	if b.mem.wing == 0 {
		b.mem.wing = 1 - 2*b.rng.Intn(2)
	}
	reach := w.flow("from-king", kingClass, seeds0([]P{king.Pos}), nil)
	score := func(p P) float64 {
		if p.Y > game.CourtRowLast {
			return -1e9
		}
		if _, ok := w.enter(kingClass, p); !ok {
			return -1e9
		}
		if u := w.unitAt(p); u != nil && !u.Mine {
			return -1e9
		}
		d := reach.at(p)
		if d >= inf {
			return -1e9
		}
		open := 0
		for _, o := range dirs {
			if _, ok := w.enter(kingClass, p.Add(o[0], o[1])); ok {
				open++
			}
		}
		away := game.Chebyshev(p, pl.front)
		if pl.safety != nil {
			away = pl.safety.at(p)
		}
		v := float64(min(away, 30)) - 0.8*float64(open) - 0.15*float64(d)
		// Сторону двора замок выбирает сам, наудачу: всегда прятать короля в
		// одном и том же углу — значит дарить гонку тому осаждающему, чей
		// лагерь ближе к нему.
		// Сторону считаем от середины замка, а не от середины карты: на
		// «Теснине» замок прижат к краю, и мерка по всей карте делала бы
		// «правую половину двора» пустым множеством.
		c := w.b.ThroneTile()
		if (p.X-c.X+p.Y-c.Y > 0) == (b.mem.wing > 0) {
			v += 2
		}
		return v
	}
	best, bestS := king.Pos, -1e9
	for y := game.CourtRowFirst; y <= game.CourtRowLast; y++ {
		for x := game.WallFirstCol; x <= game.WallLastCol; x++ {
			p := P{X: x, Y: y}
			if s := score(p); s > bestS {
				best, bestS = p, s
			}
		}
	}
	if b.mem.hasRefuge {
		if s := score(b.mem.refuge); s > -1e8 && s >= bestS-3 {
			return b.mem.refuge
		}
	}
	b.mem.refuge, b.mem.hasRefuge = best, true
	return best
}

// --- работа инженеров и лекаря ---

func (b *Bot) castleWork(w *world, pl *castlePlan, u *unit) []cand {
	var out []cand
	add := func(a game.Action, score float64) {
		a.UnitID = u.ID
		out = append(out, cand{a: a, score: score, ap: 1})
	}
	you := w.v.You
	if b.prof.mend && you.Supplies >= game.WoodGateCost {
		for _, p := range u.GateAt {
			add(game.Action{Kind: game.ActBuild, Build: "woodgate", To: p}, 16+b.pressure(w, p, 5)*8)
		}
	}
	for _, p := range u.FixAt {
		if w.tile(p) == game.TileWall {
			s := w.seg[p]
			if !b.prof.mend || s == nil || you.Fixes <= 0 || you.Supplies < game.EngineerWallCost+5 {
				continue
			}
			frac := float64(s.MaxHP-s.HP) / float64(max(s.MaxHP, 1))
			if frac < 0.25 {
				continue
			}
			add(game.Action{Kind: game.ActBuild, Build: "repair", To: p}, 3+10*frac+4*b.pressure(w, p, 7))
			continue
		}
		if you.Supplies < game.EngineerFixCost {
			continue
		}
		if t := w.unitAt(p); t != nil {
			add(game.Action{Kind: game.ActBuild, Build: "repair", To: p}, 2+5*float64(t.MaxHP-t.HP)/float64(max(t.MaxHP, 1)))
		} else if bl := w.bldAt(p); bl != nil {
			add(game.Action{Kind: game.ActBuild, Build: "repair", To: p}, 1.5+4*float64(bl.MaxHP-bl.HP)/float64(max(bl.MaxHP, 1)))
		}
	}
	for _, p := range u.HealAt {
		if t := w.unitAt(p); t != nil {
			gain := min(game.HealAmount, t.MaxHP-t.HP)
			add(game.Action{Kind: game.ActHeal, To: p}, 1.2*ownWorth(t)*float64(gain)/float64(max(t.MaxHP, 1)))
		}
	}
	return out
}

// pressure — есть ли враг в r клетках от точки: 1 — есть, 0 — нет.
func (b *Bot) pressure(w *world, p P, r int) float64 {
	for _, e := range w.foes {
		if game.Chebyshev(e.Pos, p) <= r {
			return 1
		}
	}
	return 0
}

// --- умения замка ---

func (b *Bot) castleSkills(w *world, pl *castlePlan) []cand {
	var out []cand
	you := w.v.You
	// Сегмент адресуем клеткой: на кольце столбца мало.
	ability := func(name string, at P, score float64) {
		out = append(out, cand{a: game.Action{Kind: game.ActAbility, Ability: name, At: at}, score: score, ap: 1})
	}
	// Решётку держим опущенной: открытые ворота — проход для всех.
	for p, s := range w.seg {
		if s.HP > 0 && s.Kind.IsGate() && s.Open && w.unitAt(p) == nil {
			ability("gate", p, 10)
		}
	}
	// Смола: льют под стену туда, где больше всего достанется — в первую
	// очередь подрывнику и лестнице.
	if b.prof.pitch && you.Pitches > 0 && you.Supplies >= game.PitchCost {
		for p := range w.seg {
			c := w.b.OutsideOf(p)
			sum := 0.0
			for _, e := range w.foes {
				if !w.insideCastle(e.Pos) && game.Chebyshev(e.Pos, c) <= 1 {
					sum += pl.ev.unitValue(w, e, game.PitchDamage)
				}
			}
			if sum >= 5 {
				ability("pitch", p, sum-1)
			}
		}
	}
	// Пролом: отстроить заново дорого, поэтому — когда враг рядом или
	// заколотить его деревянными воротами нечем.
	if b.prof.mend && you.Fixes > 0 && you.Supplies >= you.Rebuild {
		for p, s := range w.seg {
			if s.HP > 0 || w.unitAt(p) != nil {
				continue
			}
			score := 9 + 12*b.pressure(w, p, 6)
			if s.Kind != game.SegGate && b.engineerNear(w, p) {
				score -= 8 // инженер рядом заколотит вчетверо дешевле
			}
			ability("rebuild", p, score)
		}
	}
	if b.prof.militia && you.MilitiaOK {
		switch {
		case pl.alarm:
			ability("militia", P{}, 15)
		case pl.safety != nil && pl.king != nil && pl.safety.at(pl.king.Pos) <= 6:
			ability("militia", P{}, 8)
		}
	}
	return out
}

// engineerNear — успеет ли инженер к пролому в этом же или следующем ходу.
func (b *Bot) engineerNear(w *world, at P) bool {
	for _, u := range w.own {
		if u.spec.Builder && game.Chebyshev(u.Pos, at) <= u.spec.Move+1 {
			return true
		}
	}
	return false
}

// --- наём и стройка ---

func (b *Bot) castleWants(w *world, pl *castlePlan) []want {
	count := map[role]int{}
	for _, u := range w.own {
		if u.typ != game.UnitMilitia {
			count[pl.roles[u.ID]]++
		}
	}
	day := w.v.Day
	you := w.v.You
	post, yard := pl.front, w.b.ThroneTile()
	if top, ok := w.walkBehind(pl.front); ok {
		post = top
	}
	// Двор у фронта — клетка двора вплотную к посту: на шаг глубже боевого
	// хода, не дальше. Оттуда подкрепление успевает на стену.
	bestYard := -1
	for _, o := range dirs {
		p := post.Add(o[0], o[1])
		if w.tile(p) != game.TileCourtyard {
			continue
		}
		if d := w.wallDist(p); d > bestYard {
			yard, bestYard = p, d
		}
	}
	if pl.king != nil && pl.alarm {
		// Враг внутри — новых бойцов к нему, а не к королю: вставший
		// вплотную к королю гарнизон только замуровывает его на месте.
		best := inf
		for _, e := range pl.inside {
			if d := game.Chebyshev(e.Pos, pl.king.Pos); d < best {
				yard, best = e.Pos, d
			}
		}
	}
	breach := len(pl.threats) > 0
	engines := false
	near := false
	for _, e := range w.foes {
		if e.spec.Engine && e.typ != game.UnitLadder {
			engines = true
		}
		if e.Pos.Y <= game.WallRow+5 {
			near = true
		}
	}

	var out []want
	if b.prof.castleMine && b.mem.miner == 0 && day <= game.SiegeDays-10 {
		out = append(out, want{t: game.UnitScout, prio: 8, near: game.CastleMine()})
	}
	if b.prof.elite {
		if you.Silver >= game.Spec(game.UnitKnight).Silver && (pl.alarm || breach) {
			out = append(out, want{t: game.UnitKnight, prio: 8, near: yard})
		}
		if count[roleShooter] < b.prof.shooters+3 {
			out = append(out, want{t: game.UnitCrossbow, prio: 8.5, near: post})
		}
	}
	if count[roleShooter] < b.prof.shooters {
		prio := 6.0
		if near {
			prio = 7.5
		}
		out = append(out, want{t: game.UnitScout, prio: prio, near: post})
	}
	guards := b.prof.guards
	if pl.alarm || breach {
		guards += 2
	}
	if count[roleBlock] < guards {
		prio := 5.5
		if pl.alarm {
			prio = 8
		}
		out = append(out, want{t: game.UnitGarrison, prio: prio, near: yard})
	}
	if count[roleBuilder] < b.prof.builders {
		prio := 5.0
		if len(pl.mend) > 0 {
			prio = 7
		}
		out = append(out, want{t: game.UnitEngineer, prio: prio, near: post})
	}
	if w.count(game.UnitBallista) < b.prof.ballista && (engines || day >= 8) {
		prio := 6.5
		if engines {
			prio = 8.5
		}
		out = append(out, want{t: game.UnitBallista, prio: prio, near: post, walk: true})
	}
	if b.prof.trebuchet && w.count(game.UnitTrebuchet) == 0 && day >= 6 {
		out = append(out, want{t: game.UnitTrebuchet, prio: 5.5, near: yard})
	}
	if b.prof.healer && w.count(game.UnitHealer) == 0 && day >= 10 {
		out = append(out, want{t: game.UnitHealer, prio: 4, near: yard})
	}
	return out
}

// castleReserve — пока стоит пролом, припасы держат на то, чтобы его
// закрыть, а не на очередного стрелка.
func (b *Bot) castleReserve(w *world, pl *castlePlan) int {
	if !b.prof.mend {
		return 0
	}
	for p, s := range w.seg {
		if s.HP <= 0 {
			if s.Kind == game.SegGate {
				return w.v.You.Rebuild
			}
			_ = p
			return game.WoodGateCost
		}
	}
	return 0
}

// castleBuilds — стройка во дворе. Казармы окупаются за пять дней, и
// ставить их надо сразу; кузница нужна, когда стену начали ломать.
func (b *Bot) castleBuilds(w *world, pl *castlePlan) []cand {
	if pl.alarm || len(w.v.You.Court) == 0 {
		return nil
	}
	damaged := false
	for _, s := range w.v.Wall {
		if s.HP < s.MaxHP {
			damaged = true
		}
	}
	var out []cand
	for _, o := range w.v.You.Builds {
		if !o.Affordable {
			continue
		}
		score := 0.0
		switch o.Key {
		case "barracks":
			if o.Have < b.prof.barracks {
				score = 9
				if w.v.Day > 30 {
					score = 5
				}
			}
		case "forge":
			if b.prof.forge && o.Have == 0 && damaged {
				score = 6
			}
		}
		if score == 0 {
			continue
		}
		at, ok := b.courtSpot(w, pl)
		if !ok {
			continue
		}
		out = append(out, cand{a: game.Action{Kind: game.ActConstruct, Build: o.Key, To: at}, score: score, ap: o.AP})
	}
	return out
}

// courtSpot — клетка двора под стройку: в дальнем ряду, по флангам, рядом
// со старыми казармами. Середину двора оставляем королю: ему там ходить.
func (b *Bot) courtSpot(w *world, pl *castlePlan) (P, bool) {
	best, bestS, ok := P{}, -1e9, false
	mid := float64(game.GateColLeft+game.GateColRight) / 2
	for _, p := range w.v.You.Court {
		s := -2*float64(p.Y) + 0.4*absf(float64(p.X)-mid)
		if pl.king != nil && game.Chebyshev(p, pl.king.Pos) <= 2 {
			s -= 3
		}
		if s > bestS {
			best, bestS, ok = p, s, true
		}
	}
	return best, ok
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
