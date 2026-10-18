package game

import (
	"fmt"
	"sort"
)

type ActionKind string

const (
	ActMove      ActionKind = "move"
	ActAttack    ActionKind = "attack"
	ActDeploy    ActionKind = "deploy"
	ActClimb     ActionKind = "climb"
	ActCharge    ActionKind = "charge"
	ActBoard     ActionKind = "board"
	ActRamp      ActionKind = "ramp"
	ActUnload    ActionKind = "unload"
	ActAbility   ActionKind = "ability"
	ActBuild     ActionKind = "build"
	ActConstruct ActionKind = "construct"
	ActHeal      ActionKind = "heal"
	ActEndTurn   ActionKind = "endTurn"
)

// Action — то, что присылает клиент. Сервер проверяет всё сам.
type Action struct {
	Kind    ActionKind `json:"kind"`
	UnitID  int        `json:"unitId,omitempty"`
	To      Point      `json:"to,omitempty"`
	Unit    string     `json:"unit,omitempty"`    // тип юнита для deploy
	Ability string     `json:"ability,omitempty"` // pitch | rebuild | rally | militia | gate
	Build   string     `json:"build,omitempty"`   // trap | barricade | repair | outpost
	//        ещё для build: watchpost | woodgate | fascine | warehouse
	//        для construct: barracks | arrowTower | forge | warehouse
	// Col — столбец сегмента стены. Так кладку адресует протокол
	// «Теснины», где стена — ряд. На карте с кольцом столбца мало, и
	// сегмент называют клеткой: см. At.
	Col int `json:"col,omitempty"`
	// At — клетка сегмента стены. Если задана, Col не смотрят.
	At Point `json:"at,omitempty"`
}

// segmentOf — сегмент, к которому относится действие: по клетке, а если её
// не прислали — по столбцу, как делает старый клиент на «Теснине».
func (s *State) segmentOf(a Action) *WallSegment {
	if a.At != (Point{}) {
		return s.segment(a.At)
	}
	return s.segmentCol(a.Col)
}

// Apply — единственная точка изменения состояния.
func (s *State) Apply(side Side, a Action) error {
	if s.Phase != PhasePlay {
		return ErrPhase
	}
	if side != s.Turn {
		return ErrNotYourTurn
	}
	switch a.Kind {
	case ActEndTurn:
		s.endTurn()
		return nil
	case ActMove:
		return s.doMove(side, a)
	case ActAttack:
		return s.doAttack(side, a)
	case ActDeploy:
		return s.doDeploy(side, a)
	case ActClimb:
		return s.doClimb(side, a)
	case ActCharge:
		return s.doCharge(side, a)
	case ActBoard:
		return s.doBoard(side, a)
	case ActRamp:
		return s.doRamp(side, a)
	case ActUnload:
		return s.doUnload(side, a)
	case ActAbility:
		return s.doAbility(side, a)
	case ActBuild:
		return s.doBuild(side, a)
	case ActConstruct:
		return s.doConstruct(side, a)
	case ActHeal:
		return s.doHeal(side, a)
	}
	return fmt.Errorf("неизвестное действие %q", a.Kind)
}

func (s *State) ownUnit(side Side, id int) (*Unit, error) {
	u := s.unitByID(id)
	if u == nil {
		return nil, ErrUnitNotFound
	}
	if u.Owner != side {
		return nil, ErrBadTarget
	}
	return u, nil
}

func (s *State) spendAP(n int) error {
	if s.AP < n {
		return ErrNoAP
	}
	s.AP -= n
	return nil
}

// --- проходимость и перемещение ---

// passable говорит, может ли кто-нибудь войти в клетку p. Отряд не задан,
// поэтому и башня, и ров считаются проходимыми: для конкретного отряда есть
// passableFog.
func (s *State) passable(side Side, p Point) bool {
	if !s.b.In(p) || s.unitAt(p) != nil || s.blockedByBuilding(nil, p) {
		return false
	}
	return s.tilePassable(p)
}

// blockedByBuilding — перекрыта ли клетка постройкой для этого отряда.
// Настил из вязанок не перекрывает никого: он для того и положен, чтобы по
// нему катили машины. Наблюдательный пункт пускает внутрь только людей:
// катапульта, таран и баллиста слишком громоздки, чтобы встать в него.
func (s *State) blockedByBuilding(u *Unit, p Point) bool {
	b := s.buildingAt(p)
	if b == nil {
		return false
	}
	if b.Kind.Deck() {
		return false
	}
	if !b.Kind.Enterable() {
		return true
	}
	return u != nil && u.Spec().NoTower
}

// passableFog — то же, но чужой отряд в тумане путь не перекрывает: о нём
// ещё не знают. Столкновение обнаружится при входе на клетку.
func (s *State) passableFog(mover *Unit, p Point, vis map[Point]bool) bool {
	if !s.b.In(p) || s.blockedByBuilding(mover, p) {
		return false
	}
	// Король и его стража ходят по замку, но не наружу: приз осады не
	// убегает от осады, а храмовники не бросают приз.
	if mover.Spec().Bound && !s.b.IsInsideCastle(p) {
		return false
	}
	side := mover.Owner
	// Чужой отряд, которого мы не видим — в тумане или в засаде, — путь не
	// перекрывает: о нём ещё не знают. Столкновение обнаружится при входе.
	if u := s.unitAt(p); u != nil && (u.Owner == side || (vis[p] && !s.concealed(u, side))) {
		return false
	}
	return s.tilePassable(p)
}

// tilePassable — проходимость самой клетки, без учёта того, кто на ней стоит.
//
// Ворота здесь перестали быть «дверью для своих»: решётка — механизм, и
// пока она поднята, проём пускает **любого**. Потому вылазка и перестала
// быть бесплатной: уходя, она оставляет за собой открытый вход.
func (s *State) tilePassable(p Point) bool {
	// Открытая вода. В ров не входит никто: ни пеший вброд, ни колёсное.
	// Единственная дорога через канаву — настил из фашин, а настил это уже
	// не открытая вода. Правило тут, а не в проверке хода, чтобы в воде не
	// оказался и десант из осадной башни, и частокол полевого инженера.
	if s.moatOpen(p) {
		return false
	}
	switch s.b.TileAt(p) {
	case TileCliff, TileRavine:
		return false
	case TileWall:
		seg := s.segment(p)
		return seg != nil && seg.Passable()
	case TileWalk, TileCourtyard:
		return true // попасть сюда можно только через пролом или ворота
	}
	return true
}

// reach — поиск по стоимости: куда юнит дойдёт за свой запас перемещения и
// каким шагом он туда попадёт. Клетки стоят по-разному (куст дороже), так что
// это не простой BFS, а релаксация очередью.
//
// Второй картой идут предшественники: from[p] — клетка, с которой в p
// пришли дешевле всего. По ней doMove разворачивает путь, когда надо
// оборвать шаг на полпути. Цикла в ней быть не может: шаг стоит не меньше
// единицы, поэтому best[p] строго больше best[from[p]].
func (s *State) reach(u *Unit) (best map[Point]int, from map[Point]Point) {
	budget := u.Spec().Move
	// Пассажир осадной башни своими ногами никуда не идёт: он сходит по
	// трапу, и это отдельное действие.
	if budget <= 0 || u.movedTurn == s.turnCounter || u.Carrier != 0 {
		return nil, nil
	}
	vis := s.VisibleFor(u.Owner)
	best = map[Point]int{u.Pos: 0}
	from = map[Point]Point{}
	queue := []Point{u.Pos}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, off := range neighborOffsets {
			next := cur.Add(off[0], off[1])
			if !s.passableFog(u, next, vis) {
				continue
			}
			if !s.stepAllowed(u, cur, off, vis) {
				continue
			}
			step := s.moveCost(next)
			// Колёсная машина не скользит по диагонали, а разворачивается:
			// диагональный шаг стоит вдвое. При запасе в два очка выходит
			// ромб — прямо на две клетки, вкось на одну.
			if u.Spec().Wheeled && off[0] != 0 && off[1] != 0 {
				step *= 2
			}
			cost := best[cur] + step
			if cost > budget {
				continue
			}
			// Клетку могли уже достичь дешевле — тогда пересчитывать нечего.
			if seen, ok := best[next]; ok && seen <= cost {
				continue
			}
			best[next] = cost
			from[next] = cur
			queue = append(queue, next)
		}
	}
	return best, from
}

// Reachable — куда юнит может дойти за свой запас перемещения.
func (s *State) Reachable(u *Unit) []Point {
	best, _ := s.reach(u)
	out := make([]Point, 0, len(best))
	for p := range best {
		if p != u.Pos {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

// walkPath — путь от клетки отряда до to, без самой стартовой клетки.
// Разворачивается по карте предшественников из reach.
func walkPath(from map[Point]Point, start, to Point) []Point {
	var path []Point
	for p := to; p != start; {
		path = append(path, p)
		prev, ok := from[p]
		if !ok {
			return nil // до этой клетки пути нет
		}
		p = prev
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// stepAllowed запрещает срезать угол по диагонали между двумя непроходимыми
// клетками: протиснуться в щель между стеной и частоколом нельзя.
func (s *State) stepAllowed(mover *Unit, from Point, off [2]int, vis map[Point]bool) bool {
	if off[0] == 0 || off[1] == 0 {
		return true
	}
	return s.passableFog(mover, from.Add(off[0], 0), vis) || s.passableFog(mover, from.Add(0, off[1]), vis)
}

// sortPoints даёт стабильный порядок: по нему удобно читать снимки и логи.
func sortPoints(ps []Point) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Y != ps[j].Y {
			return ps[i].Y < ps[j].Y
		}
		return ps[i].X < ps[j].X
	})
}

func (s *State) doMove(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if u.movedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	best, from := s.reach(u)
	if _, ok := best[a.To]; !ok || a.To == u.Pos {
		return ErrBadTarget
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	u.movedTurn = s.turnCounter
	// Кто видел отряд до шага: войти в кусты незамеченным — и значит уйти
	// в засаду. Если за вами следили, заросли уже не спрячут.
	seenBefore := s.seenBy(u)

	// Разведка боем: на пути стоит отряд, которого мы не видели — в тумане
	// или в засаде. Марш обрывается о него, а не отменяется: отряд
	// проходит путь до упора и встаёт на последнюю свободную клетку,
	// вплотную к находке. Ход потрачен, засада вскрыта — но топтаться на
	// месте, как было раньше, уже не приходится.
	dest := u.Pos
	for _, p := range walkPath(from, u.Pos, a.To) {
		if blocker := s.unitAt(p); blocker != nil {
			blocker.reveal(side)
			break
		}
		dest = p
	}
	if dest == u.Pos {
		return nil // находка стояла прямо на первом шаге — идти некуда
	}
	u.Pos = dest
	// Машина везёт экипаж с собой, а трап при первом же шаге складывается:
	// держать его опущенным на ходу нельзя.
	if u.Spec().Capacity > 0 {
		u.Ramp = false
		for _, c := range s.cargoOf(u) {
			c.Pos = u.Pos
		}
	}
	s.settleCover(u, seenBefore)
	if !s.springTrap(u) {
		return nil // отряд погиб на ловушке
	}
	return nil
}

// --- атака ---

func (s *State) doAttack(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if u.actedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	spec := u.Spec()
	if spec.Damage <= 0 {
		return fmt.Errorf("%s не атакует", spec.Label)
	}
	d := Chebyshev(u.Pos, a.To)
	reach := s.unitRange(u)
	if d < 1 || d > reach {
		return fmt.Errorf("%w: дистанция %d, дальность %d", ErrBadTarget, d, reach)
	}

	// Навесное оружие бьёт по клетке, а не по цели: можно класть камни
	// вслепую, в туман, по площади.
	if spec.Splash > 0 {
		if err := s.spendAP(1); err != nil {
			return err
		}
		u.actedTurn = s.turnCounter
		s.areaStrike(side, u, a.To, spec, s.unitDamage(u))
		return nil
	}

	// Цель — стена? Но сначала смотрим, не стоит ли на этой клетке отряд:
	// в проломе и в открытых воротах стоят ногами, и такой отряд — цель,
	// а не часть кладки. Иначе он оказывался неуязвимым: удар по клетке
	// уходил в сегмент, а по пролому не уходил вовсе.
	if s.b.TileAt(a.To) == TileWall && s.unitAt(a.To) == nil {
		seg := s.segment(a.To)
		if seg == nil || seg.Breached() {
			return ErrBadTarget
		}
		if !side.IsAttacker() {
			return ErrBadTarget
		}
		if spec.VsWall <= 0 {
			return fmt.Errorf("%s не ломает стену", spec.Label)
		}
		if spec.GateOnly && !seg.Kind.IsGate() {
			return fmt.Errorf("%s бьёт только ворота", spec.Label)
		}
		if err := s.spendAP(1); err != nil {
			return err
		}
		u.actedTurn = s.turnCounter
		s.damageWall(side, seg, s.wallDamage(u))
		return nil
	}

	// Цель — отряд, занявший постройку (наблюдательный пункт): бьём по нему,
	// а не по стенам башни.
	if s.unitAt(a.To) == nil {
		if err := s.attackBuilding(side, u, a.To, spec); err != errNoBuilding {
			return err
		}
	}

	// Цель — юнит. В тумане цель не выбрать: для этого есть катапульта.
	return s.attackUnit(side, u, a.To, spec, d)
}

// errNoBuilding — внутренний признак «на клетке нет постройки»: нужен,
// чтобы одна ветка doAttack не разрослась на половину файла.
var errNoBuilding = fmt.Errorf("постройки нет")

func (s *State) attackBuilding(side Side, u *Unit, at Point, spec UnitSpec) error {
	if b := s.buildingAt(at); b != nil {
		if !areEnemies(side, b.Owner) {
			return fmt.Errorf("%w: это своя постройка", ErrBadTarget)
		}
		if spec.GateOnly {
			return fmt.Errorf("%s бьёт только ворота", spec.Label)
		}
		if err := s.spendAP(1); err != nil {
			return err
		}
		u.actedTurn = s.turnCounter
		s.damageBuilding(side, b, s.wallDamage(u))
		return nil
	}
	return errNoBuilding
}

func (s *State) attackUnit(side Side, u *Unit, at Point, spec UnitSpec, d int) error {
	if spec.NoUnits {
		return fmt.Errorf("%w: %s бьёт только по укреплениям", ErrBadTarget, spec.Label)
	}
	target := s.unitAt(at)
	if target == nil {
		return ErrBadTarget
	}
	if !areEnemies(side, target.Owner) {
		return fmt.Errorf("%w: осаждающие не бьют друг друга", ErrBadTarget)
	}
	if !s.VisibleUnit(target, side) {
		return ErrBadTarget
	}
	if !meleeReach(spec, target, d) {
		return fmt.Errorf("%w: %s достают только в рукопашной", ErrBadTarget, target.Spec().Label)
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	u.actedTurn = s.turnCounter

	// Удар из засады: цель не знала, что мы здесь. Бьём втрое — и этим же
	// себя выдаём, второй раз врасплох с той же позиции не застанешь.
	ambush := s.concealed(u, target.Owner)
	dmg := s.unitDamage(u)
	if target.Spec().Engine {
		dmg *= maxInt(spec.VsEngine, 1)
	}
	if ambush {
		dmg *= AmbushFactor
	}
	// Мантелет на линии выстрела принимает стрелу на себя: цель за ним
	// невредима, пока цел щит. В рукопашной это не работает — shieldFor
	// смотрит только на дальние удары.
	victim := target
	if shield := s.shieldFor(target, u.Pos); shield != nil {
		victim = shield
	}
	dmg = s.coverCut(dmg, victim.Pos, spec, d)
	s.hurt(victim, dmg)
	u.revealAll()
	if victim.HP <= 0 {
		// Добил — заслужил звезду: урон и прочность растут у того отряда,
		// который довёл дело до конца, а не у того, кто ранил.
		s.killUnitBy(u, victim)
	}
	return nil
}

// seenBy — кто из сторон видит отряд прямо сейчас. Снимается до шага,
// чтобы решить, спрячут ли его кусты после него.
func (s *State) seenBy(u *Unit) [4]bool {
	var out [4]bool
	for _, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
		out[side] = s.VisibleUnit(u, side)
	}
	return out
}

// settleCover решает судьбу отряда после шага: прячут его заросли или нет.
//
//   - вышел на открытое место — виден всем;
//   - вошёл в кусты под наблюдением — так и остаётся на виду;
//   - вошёл незамеченным — пропадает с чужой карты;
//   - зашёл в заросли, где уже кто-то сидит, — они находят друг друга:
//     в одном кусте не разминуться.
func (s *State) settleCover(u *Unit, seenBefore [4]bool) {
	if !s.b.InBush(u.Pos) {
		u.revealAll()
		return
	}
	for _, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
		if seenBefore[side] {
			u.reveal(side)
		} else if areEnemies(u.Owner, side) {
			u.spotted[side] = false
		} else {
			u.spotted[side] = true
		}
	}
	// Соседние кусты — те же заросли: кто в них сидит, обнаруживает себя
	// и обнаруживается сам.
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		if !s.b.InBush(p) {
			continue
		}
		other := s.unitAt(p)
		if other == nil || !areEnemies(other.Owner, u.Owner) {
			continue
		}
		other.reveal(u.Owner)
		u.reveal(other.Owner)
	}
}

// checkRegicide — единственное место, где осада кончается от удара. Король
// пал — осада окончена, и очки идут тому, чей клинок его достал. Зовётся
// из обеих точек убийства, чтобы итог не зависел от того, как именно его
// добили: из засады или в честном бою.
func (s *State) checkRegicide(killer Side, target *Unit) {
	if !target.Spec().Royal || !killer.IsAttacker() || s.Phase != PhasePlay {
		return
	}
	captor := killer
	s.finish(&captor)
}

// meleeReach — дотягивается ли этот удар до цели, которую берут только
// вплотную. Таких целей одна — король: ни стрела, ни камень катапульты его
// не достают, нужен клинок и соседняя клетка.
func meleeReach(bySpec UnitSpec, target *Unit, dist int) bool {
	if !target.Spec().MeleeOnly {
		return true
	}
	return dist <= 1 && bySpec.Range <= 1
}

// coverCut: стрелять через кусты и по башне можно, но цель за укрытием
// прикрыта — дальний удар доносит меньше. Ближний бой и смола укрытия
// игнорируют. Каменная башня держит лучше веток.
func (s *State) coverCut(dmg int, to Point, spec UnitSpec, dist int) int {
	if dist <= 1 || spec.Range <= 1 {
		return dmg
	}
	cut := 0
	switch {
	case s.inTower(to):
		cut = TowerCoverCut
	case s.b.InBush(to):
		cut = BushCoverCut
	default:
		return dmg
	}
	if left := dmg - cut; left > MinRangedHit {
		return left
	}
	return MinRangedHit
}

// areaStrike накрывает квадрат вокруг клетки: в центре полный урон, по краям
// половина. Задевает стены, постройки, врагов — и своих же: камни не
// разбирают, кто под ними. Отряды второго осаждающего правилами вне войны.
func (s *State) areaStrike(side Side, shooter *Unit, centre Point, spec UnitSpec, power int) {
	// По кладке и постройкам камень считается отдельно: у катапульты он
	// крошит их вдвое медленнее, чем бьёт по людям.
	sieged := power
	if shooter != nil {
		sieged = s.wallDamage(shooter)
	}
	for dy := -spec.Splash; dy <= spec.Splash; dy++ {
		for dx := -spec.Splash; dx <= spec.Splash; dx++ {
			p := centre.Add(dx, dy)
			if !s.b.In(p) {
				continue
			}
			dmg, hard := power, sieged
			if Chebyshev(p, centre) > 0 {
				dmg = maxInt(power/SplashEdgeDiv, MinRangedHit)
				hard = maxInt(sieged/SplashEdgeDiv, MinRangedHit)
			}

			t := s.unitAt(p)
			// Кто стоит на клетке — тот и получает камень, даже если стоит он
			// в проломе или в воротах: под ногами кладка, но целью он от этого
			// быть не перестаёт.
			if t == nil && s.b.TileAt(p) == TileWall && side.IsAttacker() && spec.VsWall > 0 {
				if seg := s.segment(p); seg != nil && !seg.Breached() {
					s.damageWall(side, seg, hard)
				}
				continue
			}
			if b := s.buildingAt(p); b != nil && t == nil {
				if areEnemies(side, b.Owner) || b.Owner == SideNeutral {
					s.damageBuilding(side, b, hard)
				}
				continue
			}
			if t == nil {
				continue
			}
			// Союзная по правилам сторона (второй осаждающий) не задевается.
			if t.Owner != side && !areEnemies(side, t.Owner) {
				continue
			}
			// Короля камнями не достать: его берут только клинком.
			if !meleeReach(spec, t, Chebyshev(shooter.Pos, p)) {
				continue
			}
			s.hurt(t, s.coverCut(dmg, p, spec, Chebyshev(shooter.Pos, p)))
			if t.HP <= 0 {
				// Камень тоже учит: накрытие засчитывается расчёту машины.
				s.killUnitBy(shooter, t)
			}
		}
	}
}

// damageWall и damageBuilding собраны отдельно: их зовут и обычная атака,
// и накрытие по площади.
func (s *State) damageWall(side Side, seg *WallSegment, dmg int) {
	seg.HP -= dmg
	s.structDamage[side] += dmg
	if seg.HP <= 0 {
		seg.HP = 0
		// В проломе решётке держаться не на чем: створки вынесло вместе
		// с кладкой.
		seg.Open = false
		s.breached[side] = true
	}
}

func (s *State) damageBuilding(side Side, b *Building, dmg int) {
	b.HP -= dmg
	if b.Owner == SideDefender {
		s.structDamage[side] += dmg
	}
	if b.HP <= 0 {
		s.removeBuilding(b)
	}
}

// killUnit — отряд снят с поля. Звёзд здесь никто не получает: так гибнут
// от ловушки, смолы и обломков, где добивать некому.
func (s *State) killUnit(killer Side, target *Unit) {
	if killer == SideDefender && target.Spec().Engine {
		s.enginesKilled++
	}
	s.removeUnit(target)
	s.spillCargo(killer, target)
	s.checkRegicide(killer, target)
}

// killUnitBy — то же, но с живым убийцей: он получает звезду ветерана.
// Единственная точка, где отряды растут, — и зовётся она и из рукопашной,
// и из накрытия, и из-под трапа осадной башни.
func (s *State) killUnitBy(by *Unit, target *Unit) {
	s.promote(by)
	s.killUnit(by.Owner, target)
}

func areEnemies(a, b Side) bool {
	if a == b {
		return false
	}
	// Осаждающие соперничают, но друг друга не атакуют: гонка идёт за пролом.
	return !(a.IsAttacker() && b.IsAttacker())
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- наём ---

func (s *State) doDeploy(side Side, a Action) error {
	t, ok := UnitTypeByName(a.Unit)
	if !ok || !canBuy(side, t) {
		return fmt.Errorf("%w: нельзя нанять %q", ErrBadTarget, a.Unit)
	}
	spec := Spec(t)
	if s.HiresLeft(side) <= 0 {
		return fmt.Errorf("%w: за один ход нанимают не больше %d отрядов", ErrBadTarget, MaxDeploysPerTurn)
	}
	if cap := ArmyCap(side); s.ArmySize(side) >= cap {
		return fmt.Errorf("%w: на поле уже %d отрядов — это предел", ErrBadTarget, cap)
	}
	if spec.MaxAlive > 0 && s.countUnits(side, t) >= spec.MaxAlive {
		return fmt.Errorf("%w: %s больше %d одновременно не держат", ErrBadTarget, spec.Label, spec.MaxAlive)
	}
	cost := s.unitCost(side, t)
	if s.supplies[side] < cost {
		return ErrNoSupplies
	}
	if s.silver[side] < spec.Silver {
		return fmt.Errorf("%w: нужно %d, есть %d — удерживайте рудник", ErrNoSilver, spec.Silver, s.silver[side])
	}
	if !s.deployZone(side, a.To) {
		return fmt.Errorf("%w: не ваша зона развёртывания", ErrBadTarget)
	}
	// Осадную башню собирают на месте, из брёвен и досок: аванпост такую
	// махину не выкатит, её везут из лагеря.
	if spec.CampOnly && s.b.CampSide(a.To) != side {
		return fmt.Errorf("%w: %s собирают только в лагере", ErrBadTarget, spec.Label)
	}
	if s.unitAt(a.To) != nil {
		return fmt.Errorf("%w: клетка занята", ErrBadTarget)
	}
	if err := s.spendAP(DeployAPCost); err != nil {
		return err
	}
	s.supplies[side] -= cost
	s.silver[side] -= spec.Silver
	s.noteHire(side)
	u := s.spawn(side, t, a.To)
	u.movedTurn = s.turnCounter // в день найма юнит не ходит
	u.actedTurn = s.turnCounter
	return nil
}

func (s *State) deployZone(side Side, p Point) bool {
	if side == SideDefender {
		switch s.b.TileAt(p) {
		case TileWalk, TileCourtyard:
			return true
		}
		return false
	}
	// Осаждающий нанимает в своём лагере — или у собственного аванпоста,
	// если успел его поставить.
	if s.b.CampSide(p) == side {
		return true
	}
	return s.outpostNear(side, p) && s.outpostDeployable(p)
}

// outpostDeployable — куда можно вывести отряд из аванпоста: только открытое
// поле. Внутрь замка и на стену аванпост никого не телепортирует, в чужой
// лагерь — тоже, а рудник по-прежнему берут ногами, а не наймом.
func (s *State) outpostDeployable(p Point) bool {
	switch s.b.TileAt(p) {
	case TileField, TileBush:
		return s.buildingAt(p) == nil
	}
	return false
}

// --- штурм по лестнице ---

// doClimb — подъём на стену по приставной лестнице. Лестницу подкатывают
// вплотную к стене, и дальше наверх идёт **любой пеший отряд** осаждающих:
// машины остаются внизу, лезть по перекладинам им нечем.
//
// Подъём считается действием, а не перемещением. Иначе лестницей нельзя было
// пользоваться вовсе: подойти к ней и влезть в один день не получалось, а
// простоять день вплотную к стене под смолой удаётся не всем.
func (s *State) doClimb(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if !side.IsAttacker() || u.Spec().Mechanism() {
		return fmt.Errorf("%w: по лестнице лезут пешие отряды, машины остаются внизу", ErrBadTarget)
	}
	if u.actedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	ladder := s.adjacentLadder(u.Pos)
	if ladder == nil {
		return fmt.Errorf("%w: рядом нет лестницы, приставленной к стене", ErrBadTarget)
	}
	top, ok := s.ladderTop(ladder)
	if !ok {
		return fmt.Errorf("%w: лестница ни к чему не приставлена", ErrBadTarget)
	}
	if s.unitAt(top) != nil {
		return fmt.Errorf("%w: на стене занято", ErrBadTarget)
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	u.Pos = top
	u.movedTurn = s.turnCounter
	u.actedTurn = s.turnCounter
	u.revealAll()
	return nil
}

// ladderTop — куда выводит лестница: боевой ход за тем сегментом, к
// которому она приставлена.
func (s *State) ladderTop(ladder *Unit) (Point, bool) {
	seg := s.wallBeside(ladder.Pos)
	if seg == nil {
		return Point{}, false
	}
	return s.b.walkBehind(seg.Pos)
}

// adjacentLadder ищет лестницу рядом с юнитом, приставленную к стене.
func (s *State) adjacentLadder(from Point) *Unit {
	for _, off := range neighborOffsets {
		p := from.Add(off[0], off[1])
		u := s.unitAt(p)
		if u == nil || u.Type != UnitLadder {
			continue
		}
		if s.wallBeside(u.Pos) != nil {
			return u
		}
	}
	return nil
}

// --- осадная башня: посадка, трап, высадка ---

// boardTargets — в какие свои осадные башни этот отряд может сесть прямо
// сейчас. Внутрь берут только пеших: машину в машину не грузят.
func (s *State) boardTargets(u *Unit) []Point {
	if u.Carrier != 0 || u.Spec().Mechanism() || u.movedTurn == s.turnCounter {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		t := s.unitAt(u.Pos.Add(off[0], off[1]))
		if t == nil || t.Owner != u.Owner || t.Spec().Capacity <= 0 {
			continue
		}
		if len(s.cargoOf(t)) >= t.Spec().Capacity {
			continue // башня полна
		}
		out = append(out, t.Pos)
	}
	sortPoints(out)
	return out
}

func (s *State) doBoard(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if u.Carrier != 0 {
		return fmt.Errorf("%w: отряд уже внутри", ErrBadTarget)
	}
	if u.Spec().Mechanism() {
		return fmt.Errorf("%w: машину внутрь башни не затащить", ErrBadTarget)
	}
	if u.movedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	if !containsPoint(s.boardTargets(u), a.To) {
		return fmt.Errorf("%w: рядом нет своей осадной башни со свободным местом", ErrBadTarget)
	}
	tower := s.unitAt(a.To)
	if err := s.spendAP(BoardAPCost); err != nil {
		return err
	}
	u.movedTurn = s.turnCounter
	u.Carrier = tower.ID
	u.Pos = tower.Pos
	return nil
}

// canRamp — можно ли опустить трап. Башню для этого надо подвести вплотную
// к стене: трап перекидывают на боевой ход, а не на пустое поле.
func (s *State) canRamp(u *Unit) bool {
	if u.Spec().Capacity <= 0 || u.Ramp || u.actedTurn == s.turnCounter {
		return false
	}
	return s.wallBeside(u.Pos) != nil
}

// wallBeside — сегмент кладки вплотную к клетке, если смотреть снаружи. По
// нему лезут на стену с лестницы и с осадной башни. На «Теснине» стена
// всегда над головой, на кольце — с любой стороны, поэтому смотрим по
// сторонам, а не вверх; изнутри замка приставлять лестницу незачем.
func (s *State) wallBeside(p Point) *WallSegment {
	if s.b.IsInsideCastle(p) {
		return nil
	}
	for _, d := range [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
		if seg := s.segment(p.Add(d[0], d[1])); seg != nil {
			return seg
		}
	}
	return nil
}

func (s *State) doRamp(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if u.Spec().Capacity <= 0 {
		return fmt.Errorf("%w: трап есть только у осадной башни", ErrBadTarget)
	}
	if u.Ramp {
		return fmt.Errorf("%w: трап уже опущен", ErrBadTarget)
	}
	if u.actedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	if !s.canRamp(u) {
		return fmt.Errorf("%w: башню надо подвести вплотную к стене", ErrBadTarget)
	}
	if err := s.spendAP(RampAPCost); err != nil {
		return err
	}
	u.actedTurn = s.turnCounter
	u.Ramp = true
	s.crushUnderRamp(u)
	return nil
}

// crushUnderRamp — трап падает на боевой ход всей своей массой. Кто стоял на
// клетке, куда он лёг, гибнет на месте: окованный помост весом в несколько
// пудов не отбивают щитом.
//
// Короля это не берёт: его достают только клинком, и подменять рукопашную
// падающей доской нельзя — иначе осадная башня выигрывала бы осаду одним
// действием, стоило королю оказаться на стене.
func (s *State) crushUnderRamp(tower *Unit) {
	top, ok := s.rampTop(tower)
	if !ok {
		return
	}
	victim := s.unitAt(top)
	if victim == nil || !areEnemies(tower.Owner, victim.Owner) {
		return
	}
	if victim.Spec().MeleeOnly {
		return
	}
	s.killUnitBy(tower, victim)
}

// unloadSpots — куда экипаж сходит с опущенного трапа: на боевой ход, в тот
// же столбец или в соседний. Через стену, а не через пролом — в этом вся
// затея.
func (s *State) unloadSpots(tower *Unit) []Point {
	if tower.Spec().Capacity <= 0 || !tower.Ramp {
		return nil
	}
	top, ok := s.rampTop(tower)
	if !ok {
		return nil
	}
	// Сходят в клетку трапа и в соседние с ней по боевому ходу: не дальше
	// одного шага вдоль стены.
	var out []Point
	for _, d := range neighborOffsets {
		p := top.Add(d[0], d[1])
		if p != top && (s.b.TileAt(p) != TileWalk || Chebyshev(p, top) > 1) {
			continue
		}
		if s.unitAt(p) != nil || s.blockedByBuilding(nil, p) {
			continue
		}
		out = append(out, p)
	}
	if s.unitAt(top) == nil && !s.blockedByBuilding(nil, top) {
		out = append(out, top)
	}
	sortPoints(out)
	return out
}

// rampTop — куда ложится трап осадной башни: боевой ход за тем сегментом, к
// которому башня подведена.
func (s *State) rampTop(tower *Unit) (Point, bool) {
	seg := s.wallBeside(tower.Pos)
	if seg == nil {
		return Point{}, false
	}
	return s.b.walkBehind(seg.Pos)
}

// exitSpots — куда экипаж может просто вылезти из башни: на соседние клетки
// снаружи. Трап для этого не нужен — в люк выбираются и под обстрелом, —
// зато так не попасть ни на стену, ни во двор: туда только по трапу.
//
// Без этого выхода башня была ловушкой: подбитая на подходе, она хоронила
// четверых, и единственным способом спасти их было её не трогать вовсе.
func (s *State) exitSpots(tower *Unit) []Point {
	if tower.Spec().Capacity <= 0 {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		p := tower.Pos.Add(off[0], off[1])
		if !s.b.In(p) || s.b.TileAt(p) == TileWall || s.b.IsInsideCastle(p) {
			continue
		}
		if s.unitAt(p) != nil || s.blockedByBuilding(nil, p) || !s.tilePassable(p) {
			continue
		}
		out = append(out, p)
	}
	sortPoints(out)
	return out
}

func (s *State) doUnload(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if u.Carrier == 0 {
		return fmt.Errorf("%w: этот отряд не в башне", ErrBadTarget)
	}
	tower := s.unitByID(u.Carrier)
	if tower == nil {
		return ErrBadTarget
	}
	if u.movedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	// Два выхода разом: по трапу — на боевой ход, через люк — на землю
	// рядом с башней. Какой из них сработал, решает сама клетка.
	onWall := containsPoint(s.unloadSpots(tower), a.To)
	if !onWall && !containsPoint(s.exitSpots(tower), a.To) {
		if !tower.Ramp {
			return fmt.Errorf("%w: трап не опущен — вылезти можно только на соседнюю клетку", ErrBadTarget)
		}
		return fmt.Errorf("%w: здесь не встать", ErrBadTarget)
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	u.movedTurn = s.turnCounter
	u.Carrier = 0
	u.Pos = a.To
	u.revealAll()
	s.springTrap(u)
	return nil
}

// spillCargo — что остаётся от экипажа, когда башню разбили. Половина
// полного здоровья каждому: падение с помоста не разбирает, кто был ранен,
// а кто цел. Выжившие спрыгивают на соседние клетки; кому прыгать некуда —
// остаётся в обломках.
func (s *State) spillCargo(killer Side, carrier *Unit) {
	if carrier.Spec().Capacity <= 0 {
		return
	}
	for _, u := range s.cargoOf(carrier) {
		u.Carrier = 0
		s.hurt(u, wreckDamage(u))
		spot, ok := s.escapeSpot(u, carrier.Pos)
		if u.HP <= 0 || !ok {
			// Либо добило падением, либо выбираться некуда: обломки
			// держат не хуже стены.
			s.removeUnit(u)
			continue
		}
		u.Pos = spot
		u.revealAll()
	}
}

// escapeSpot — куда спрыгнуть из разбитой башни: сначала на её же клетку,
// потом на любую соседнюю проходимую.
func (s *State) escapeSpot(u *Unit, from Point) (Point, bool) {
	try := func(p Point) bool {
		return s.b.In(p) && s.unitAt(p) == nil && !s.blockedByBuilding(u, p) &&
			s.tilePassable(p)
	}
	if try(from) {
		return from, true
	}
	for _, off := range neighborOffsets {
		if p := from.Add(off[0], off[1]); try(p) {
			return p, true
		}
	}
	return Point{}, false
}

// --- подрыв стены ---

// chargeTargets — под какие сегменты этот подрывник может заложить заряд
// прямо сейчас. Работает он вплотную, но вплотную к нему может оказаться
// несколько сегментов разом — какой ронять, решает игрок, а не геометрия.
func (s *State) chargeTargets(u *Unit) []Point {
	if u.Charges <= 0 || u.actedTurn == s.turnCounter {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		if !s.b.In(p) || s.b.TileAt(p) != TileWall {
			continue
		}
		if seg := s.segment(p); seg != nil && !seg.Breached() {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

// doCharge — подрывник закладывает заряд под выбранный сегмент. Одного
// заряда хватает на любой из них: стена складывается сразу, целиком. Взрыв
// забирает и самого подрывника — поэтому это не осадная работа, а один
// рывок к стене.
func (s *State) doCharge(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if u.Charges <= 0 {
		return fmt.Errorf("%w: заряды закладывает только подрывник", ErrBadTarget)
	}
	if u.actedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	targets := s.chargeTargets(u)
	if len(targets) == 0 {
		return fmt.Errorf("%w: рядом нет целого сегмента стены", ErrBadTarget)
	}
	at := a.To
	// Старые клиенты присылали только номер столбца — поддерживаем и его.
	if a.Col > 0 && at == (Point{}) {
		at = Point{a.Col, WallRow}
	}
	if !containsPoint(targets, at) {
		return fmt.Errorf("%w: заряд закладывают под соседний целый сегмент", ErrBadTarget)
	}
	seg := s.segment(at)
	if err := s.spendAP(1); err != nil {
		return err
	}
	u.actedTurn = s.turnCounter
	u.Charges--

	s.damageWall(side, seg, seg.HP)
	s.removeUnit(u) // заряд забирает и того, кто его закладывал
	return nil
}

func containsPoint(ps []Point, p Point) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

// springTrap проверяет ловушку под ногами. Возвращает false, если отряд погиб.
func (s *State) springTrap(u *Unit) bool {
	trap := s.trapAt(u.Pos)
	if trap == nil || !areEnemies(trap.Owner, u.Owner) {
		return true
	}
	owner := trap.Owner
	s.removeTrap(u.Pos)
	s.hurt(u, TrapDamage)
	if u.HP <= 0 {
		s.killUnit(owner, u)
		return false
	}
	return true
}

// --- инженеры ---

func (s *State) doBuild(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if u.actedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	// Фашины разбираем до общей проверки соседства: вязанки кидают и под
	// себя — стоя в канаве, сбрасывать их в неё как раз удобнее всего.
	if a.Build == "fascine" {
		return s.fillMoat(side, u, a.To)
	}
	if Chebyshev(u.Pos, a.To) != 1 {
		return fmt.Errorf("%w: строить можно только на соседней клетке", ErrBadTarget)
	}
	// Аванпост разбивают сами штурмовые отряды, а не инженеры замка.
	if a.Build == "outpost" {
		return s.buildOutpost(side, u, a.To)
	}
	// Наблюдательный пункт ставят обе стороны, но разными руками: у замка —
	// инженеры, у осаждающих — пехота.
	if a.Build == "watchpost" {
		return s.buildWatchPost(side, u, a.To)
	}
	// Склад осаждающие ставят в своём лагере, замок — во дворе (construct).
	if a.Build == "warehouse" {
		return s.buildCampWarehouse(side, u, a.To)
	}
	// Мост ничей, и чинят его осаждающие: замку переправа к собственным
	// воротам не нужна — он её ломает. Весь прочий ремонт ниже, в умениях
	// инженеров замка, и осаде туда хода нет.
	if a.Build == "repair" && side.IsAttacker() {
		return s.repairBridge(side, u, a.To)
	}
	// Частокол и мантелет — работа полевых инженеров осады, и только их.
	switch a.Build {
	case "palisade":
		return s.buildPalisade(side, u, a.To)
	case "mantlet":
		return s.buildMantlet(side, u, a.To)
	}
	if !u.Spec().Builder {
		return fmt.Errorf("%w: строить умеют только инженеры", ErrBadTarget)
	}
	// Дальше идут умения инженеров **замка**: ловушки, частокол, ремонт и
	// деревянные ворота. Полевым инженерам осады они недоступны.
	if side != SideDefender {
		return fmt.Errorf("%w: это умения инженеров замка", ErrBadTarget)
	}

	switch a.Build {
	case "trap":
		return s.buildTrap(side, u, a.To)
	case "barricade":
		return s.buildBarricade(side, u, a.To)
	case "repair":
		return s.engineerRepair(side, u, a.To)
	case "woodgate":
		return s.buildWoodGate(side, u, a.To)
	}
	return fmt.Errorf("%w: инженеры такого не строят: %q", ErrBadTarget, a.Build)
}

// --- ров и фашины ---

// canFascine — может ли отряд забрасывать ров. Хворост вяжут и носят люди:
// машины этим не заняты, а защитнику свой ров засыпать незачем.
func (s *State) canFascine(u *Unit) bool {
	return u.Owner.IsAttacker() && !u.Spec().Mechanism() && u.actedTurn != s.turnCounter
}

// fascineAP — во сколько отряду обходится вязанка. Пехоте — даром: носить
// хворост её работа, и отдельного очка действия на это не нужно. Отряд всё
// равно тратит на переправу своё действие, так что больше одной клетки за
// ход он не закроет.
func fascineAP(u *Unit) int {
	if u.Type == UnitInfantry {
		return FascineAPFoot
	}
	return FascineAPCost
}

// fascineSpots — какие клетки рва этот отряд может завалить прямо сейчас:
// соседние и ещё не засыпанные. Только соседние: в самой воде не стоят,
// вязанки сбрасывают с берега или с готового настила.
func (s *State) fascineSpots(u *Unit) []Point {
	if !s.canFascine(u) {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		if p := u.Pos.Add(off[0], off[1]); s.b.In(p) && s.moatOpen(p) {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

// fillMoat — забросать клетку рва фашинами. По клетке за действие: подвести
// таран или осадную башню к стене теперь отдельная работа на несколько
// дней, и видно её защитнику издалека.
func (s *State) fillMoat(side Side, u *Unit, at Point) error {
	if !side.IsAttacker() {
		return fmt.Errorf("%w: ров засыпают осаждающие", ErrBadTarget)
	}
	if u.Spec().Mechanism() {
		return fmt.Errorf("%w: фашины носят отряды, а не машины", ErrBadTarget)
	}
	if u.actedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	if !containsPoint(s.fascineSpots(u), at) {
		return fmt.Errorf("%w: засыпать можно только соседнюю открытую клетку рва", ErrBadTarget)
	}
	if err := s.spendAP(fascineAP(u)); err != nil {
		return err
	}
	u.actedTurn = s.turnCounter
	s.fillMoatAt(at, side)
	return nil
}

// --- частокол и мантелеты: работа полевых инженеров осады ---

// canSap — полевой инженер осаждающих, которому есть чем заняться.
func (s *State) canSap(u *Unit) bool {
	return u.Owner.IsAttacker() && u.Type == UnitSapper && u.actedTurn != s.turnCounter
}

func (s *State) buildPalisade(side Side, u *Unit, at Point) error {
	if !s.canSap(u) {
		return fmt.Errorf("%w: частокол ставят полевые инженеры осады", ErrBadTarget)
	}
	if !s.outdoorBuildable(at) {
		return fmt.Errorf("%w: здесь частокол не поставить", ErrBadTarget)
	}
	if s.supplies[side] < PalisadeCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= PalisadeCost
	u.actedTurn = s.turnCounter
	s.Buildings = append(s.Buildings, &Building{
		ID: s.nextBuildingID, Kind: BuildPalisade, Owner: side, Pos: at,
		HP: PalisadeHP, MaxHP: PalisadeHP,
	})
	s.nextBuildingID++
	return nil
}

// mantletSpots — куда можно выкатить собранный щит. Это не постройка, а
// машина, поэтому клетка нужна такая, где она может стоять: открытое поле,
// кусты, лагерь — и ров тоже, раму несут на руках. Внутрь замка и на стену
// мантелет не ставят.
func (s *State) mantletSpots(u *Unit) []Point {
	if !s.canSap(u) {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		if !s.b.In(p) || s.b.IsInsideCastle(p) || s.b.TileAt(p) == TileWall {
			continue
		}
		if s.unitAt(p) != nil || s.blockedByBuilding(nil, p) || !s.tilePassable(p) {
			continue
		}
		out = append(out, p)
	}
	sortPoints(out)
	return out
}

// buildMantlet — собрать передвижной щит на соседней клетке. Он выходит
// готовым к бою, но в этот день никуда не катится: раму ещё сколачивают.
func (s *State) buildMantlet(side Side, u *Unit, at Point) error {
	if !s.canSap(u) {
		return fmt.Errorf("%w: мантелеты собирают полевые инженеры осады", ErrBadTarget)
	}
	if !containsPoint(s.mantletSpots(u), at) {
		return fmt.Errorf("%w: щит выкатывают на свободную соседнюю клетку", ErrBadTarget)
	}
	spec := Spec(UnitMantlet)
	if n := s.countUnits(side, UnitMantlet); n >= spec.MaxAlive {
		return fmt.Errorf("%w: больше %d мантелетов разом не держат", ErrBadTarget, spec.MaxAlive)
	}
	if cap := ArmyCap(side); s.ArmySize(side) >= cap {
		return fmt.Errorf("%w: на поле уже %d отрядов — это предел", ErrBadTarget, cap)
	}
	if s.supplies[side] < MantletCost {
		return fmt.Errorf("%w: нужно %d, есть %d", ErrNoSupplies, MantletCost, s.supplies[side])
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= MantletCost
	u.actedTurn = s.turnCounter
	m := s.spawn(side, UnitMantlet, at)
	m.movedTurn, m.actedTurn = s.turnCounter, s.turnCounter
	return nil
}

// --- склад в лагере ---

// canCampWarehouse — может ли этот отряд поставить склад. У осаждающих это
// пехота, и только в своём лагере: обоз не разбивают в чистом поле.
func (s *State) canCampWarehouse(u *Unit) bool {
	return u.Owner.IsAttacker() && u.Type == UnitInfantry &&
		u.actedTurn != s.turnCounter &&
		s.countBuildings(u.Owner, BuildWarehouse) < MaxWarehouse
}

// campWarehouseSpots — соседние клетки своего лагеря под склад.
func (s *State) campWarehouseSpots(u *Unit) []Point {
	if !s.canCampWarehouse(u) {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		if s.b.In(p) && s.b.CampSide(p) == u.Owner && s.outdoorBuildable(p) {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

func (s *State) buildCampWarehouse(side Side, u *Unit, at Point) error {
	if !side.IsAttacker() {
		return fmt.Errorf("%w: замок строит склад во дворе", ErrBadTarget)
	}
	if u.Type != UnitInfantry {
		return fmt.Errorf("%w: склад в лагере ставит пехота", ErrBadTarget)
	}
	if n := s.countBuildings(side, BuildWarehouse); n >= MaxWarehouse {
		return fmt.Errorf("%w: складов больше %d не держат", ErrBadTarget, MaxWarehouse)
	}
	if s.b.CampSide(at) != side || !s.outdoorBuildable(at) {
		return fmt.Errorf("%w: склад ставят на свободной клетке своего лагеря", ErrBadTarget)
	}
	if s.supplies[side] < WarehouseCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= WarehouseCost
	u.actedTurn = s.turnCounter
	s.Buildings = append(s.Buildings, &Building{
		ID: s.nextBuildingID, Kind: BuildWarehouse, Owner: side, Pos: at,
		HP: WarehouseHP, MaxHP: WarehouseHP,
	})
	s.nextBuildingID++
	return nil
}

// outdoorBuildable — клетки, где инженеры могут что-то поставить: только
// открытое поле, не стена и не двор.
func (s *State) outdoorBuildable(p Point) bool {
	switch s.b.TileAt(p) {
	case TileField, TileBush, TileMine, TileFarm, TileCamp:
	default:
		return false
	}
	return s.unitAt(p) == nil && s.buildingAt(p) == nil && s.trapAt(p) == nil
}

func (s *State) buildTrap(side Side, u *Unit, at Point) error {
	if !s.outdoorBuildable(at) {
		return fmt.Errorf("%w: здесь ловушку не поставить", ErrBadTarget)
	}
	if s.supplies[side] < TrapCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= TrapCost
	u.actedTurn = s.turnCounter
	s.Traps = append(s.Traps, Trap{Pos: at, Owner: side})
	return nil
}

// fenceLine — куда встанет частокол замка при клике по клетке at от отряда,
// стоящего в from. Инженеры ставят не кол, а прясло: BarricadeSpan клеток в
// линию **поперёк** направления «инженер → цель». Клик по клетке над собой
// даёт заслон в три клетки шириной перед инженером, клик по углу — такую же
// линию по диагонали.
//
// Первой в списке идёт сама цель: она обязательна, остальные клетки —
// добавка. Где кол не встаёт (занято, стена, двор, вода), клетка просто
// выпадает из линии, и цена от этого не меняется: платят за работу, а не
// за колья.
func (s *State) fenceLine(from, at Point) []Point {
	out := []Point{at}
	dx, dy := sign(at.X-from.X), sign(at.Y-from.Y)
	if dx == 0 && dy == 0 {
		return out
	}
	// Поворот направления на 90°: вдоль этой оси и тянется прясло.
	px, py := -dy, dx
	for i := 1; i <= (BarricadeSpan-1)/2; i++ {
		for _, sgn := range []int{-1, 1} {
			p := at.Add(px*i*sgn, py*i*sgn)
			if s.b.In(p) && s.outdoorBuildable(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// buildBarricade — поставить частокол замка: прясло в BarricadeSpan клеток
// за одно действие и одну цену. Клетка под курсором обязана быть годной,
// остальные — как получится, см. fenceLine.
func (s *State) buildBarricade(side Side, u *Unit, at Point) error {
	if !s.outdoorBuildable(at) {
		return fmt.Errorf("%w: здесь частокол не поставить", ErrBadTarget)
	}
	if s.supplies[side] < BarricadeCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= BarricadeCost
	u.actedTurn = s.turnCounter
	for _, p := range s.fenceLine(u.Pos, at) {
		s.Buildings = append(s.Buildings, &Building{
			ID: s.nextBuildingID, Kind: BuildBarricade, Owner: side, Pos: p,
			HP: BarricadeHP, MaxHP: BarricadeHP,
		})
		s.nextBuildingID++
	}
	return nil
}

// --- деревянные ворота ---

// woodGateSpots — в какие проломы этот инженер может вставить деревянные
// ворота. На месте замковых ворот — нельзя: там и так ворота, и подменять
// их створкой на пятнадцать прочности нечестно по отношению к тарану.
func (s *State) woodGateSpots(u *Unit) []Point {
	if !u.Spec().Builder || u.Owner != SideDefender || u.actedTurn == s.turnCounter {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		if !s.b.In(p) || s.b.TileAt(p) != TileWall || s.b.IsCastleGate(p) {
			continue
		}
		seg := s.segment(p)
		if seg == nil || !seg.Breached() || s.unitAt(p) != nil {
			continue
		}
		out = append(out, p)
	}
	sortPoints(out)
	return out
}

// buildWoodGate — заколотить пролом деревянными воротами. Вчетверо дешевле
// отстройки и доступно прямо с боевого хода, но держит такая створка один
// хороший удар: это способ закрыть дыру на сегодня, а не починить стену.
func (s *State) buildWoodGate(side Side, u *Unit, at Point) error {
	if side != SideDefender {
		return fmt.Errorf("%w: ворота ставит замок", ErrBadTarget)
	}
	if s.b.IsCastleGate(at) {
		return fmt.Errorf("%w: на месте замковых ворот деревянные не ставят", ErrBadTarget)
	}
	if !containsPoint(s.woodGateSpots(u), at) {
		return fmt.Errorf("%w: деревянные ворота вставляют в соседний пустой пролом", ErrBadTarget)
	}
	if s.supplies[side] < WoodGateCost {
		return fmt.Errorf("%w: нужно %d, есть %d", ErrNoSupplies, WoodGateCost, s.supplies[side])
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= WoodGateCost
	u.actedTurn = s.turnCounter
	seg := s.segment(at)
	seg.Kind = SegWoodGate
	seg.HP, seg.MaxHP = WoodGateHP, WoodGateHP
	seg.Open = false
	return nil
}

// --- наблюдательные пункты ---

// canWatchPost — может ли этот отряд поставить вышку. У замка это полевые
// инженеры, у осаждающих — пехота: тащить брёвна через поле и сколачивать
// помост больше некому, а машины и стрелки заняты своим делом.
func (s *State) canWatchPost(u *Unit) bool {
	if u.actedTurn == s.turnCounter {
		return false
	}
	if s.countBuildings(u.Owner, BuildTower) >= WatchPostMax {
		return false
	}
	if u.Owner == SideDefender {
		return u.Spec().Builder
	}
	// У осаждающих вышки ставят пехота и полевые инженеры: первым не впервой
	// таскать брёвна, вторым это прямая работа.
	return u.Owner.IsAttacker() && (u.Type == UnitInfantry || u.Type == UnitSapper)
}

// watchPostSpots — соседние клетки, куда этот отряд может поставить вышку.
func (s *State) watchPostSpots(u *Unit) []Point {
	if !s.canWatchPost(u) {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		if p := u.Pos.Add(off[0], off[1]); s.b.In(p) && s.outdoorBuildable(p) {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

func (s *State) buildWatchPost(side Side, u *Unit, at Point) error {
	if u.Owner == SideDefender && !u.Spec().Builder {
		return fmt.Errorf("%w: вышки замка ставят полевые инженеры", ErrBadTarget)
	}
	if u.Owner.IsAttacker() && u.Type != UnitInfantry && u.Type != UnitSapper {
		return fmt.Errorf("%w: вышки осаждающих ставят пехота и полевые инженеры", ErrBadTarget)
	}
	if n := s.countBuildings(side, BuildTower); n >= WatchPostMax {
		return fmt.Errorf("%w: больше %d наблюдательных пунктов на сторону не держат",
			ErrBadTarget, WatchPostMax)
	}
	if !s.outdoorBuildable(at) {
		return fmt.Errorf("%w: вышку ставят на свободной клетке открытого поля", ErrBadTarget)
	}
	cost := WatchPostCost(side)
	if s.supplies[side] < cost {
		return fmt.Errorf("%w: нужно %d, есть %d", ErrNoSupplies, cost, s.supplies[side])
	}
	if err := s.spendAP(WatchPostAPCost); err != nil {
		return err
	}
	s.supplies[side] -= cost
	u.actedTurn = s.turnCounter
	hp := buildingHP(BuildTower)
	s.Buildings = append(s.Buildings, &Building{
		ID: s.nextBuildingID, Kind: BuildTower, Owner: side, Pos: at, HP: hp, MaxHP: hp,
	})
	s.nextBuildingID++
	return nil
}

// --- аванпост осаждающих ---

// canOutpost — может ли этот отряд разбить аванпост. Машины не умеют:
// лагерь ставят люди.
func (s *State) canOutpost(u *Unit) bool {
	if !u.Owner.IsAttacker() || u.Spec().Mechanism() {
		return false
	}
	if u.actedTurn == s.turnCounter {
		return false
	}
	return s.countBuildings(u.Owner, BuildOutpost) < OutpostMax
}

// outpostSpot — годится ли клетка под аванпост. Только открытое поле и не
// вплотную к стене: аванпост — тыловой лагерь, а не штурмовая площадка.
func (s *State) outpostSpot(p Point) bool {
	switch s.b.TileAt(p) {
	case TileField, TileBush:
	default:
		return false
	}
	if s.b.wallDistance(p) < OutpostWallGap {
		return false
	}
	return s.unitAt(p) == nil && s.buildingAt(p) == nil && s.trapAt(p) == nil
}

// outpostSpots — соседние клетки, куда этот отряд может поставить аванпост.
func (s *State) outpostSpots(u *Unit) []Point {
	if !s.canOutpost(u) {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		if p := u.Pos.Add(off[0], off[1]); s.b.In(p) && s.outpostSpot(p) {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

func (s *State) buildOutpost(side Side, u *Unit, at Point) error {
	if !side.IsAttacker() {
		return fmt.Errorf("%w: аванпосты ставят только осаждающие", ErrBadTarget)
	}
	if u.Spec().Mechanism() {
		return fmt.Errorf("%w: лагерь разбивают отряды, а не машины", ErrBadTarget)
	}
	if n := s.countBuildings(side, BuildOutpost); n >= OutpostMax {
		return fmt.Errorf("%w: аванпост уже стоит (не больше %d)", ErrBadTarget, OutpostMax)
	}
	if !s.outpostSpot(at) {
		return fmt.Errorf("%w: аванпост ставят на открытом поле не ближе %d рядов от стены",
			ErrBadTarget, OutpostWallGap)
	}
	if s.supplies[side] < OutpostCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(OutpostAPCost); err != nil {
		return err
	}
	s.supplies[side] -= OutpostCost
	u.actedTurn = s.turnCounter
	s.Buildings = append(s.Buildings, &Building{
		ID: s.nextBuildingID, Kind: BuildOutpost, Owner: side, Pos: at,
		HP: OutpostHP, MaxHP: OutpostHP,
		// Стоит ровно OutpostLife дней, считая сегодняшний.
		Expires: s.Day + OutpostLife - 1,
	})
	s.nextBuildingID++
	return nil
}

// --- ремонт инженерами ---

// engineerRepair — инженеры чинят всё, что стоит рядом: кладку и ворота,
// свои постройки и свои осадные машины.
//
// Кладку они латают, но не поднимают: пока сегмент стоит, его можно
// подпереть и залить раствором на пятую часть полной прочности, а лежащий
// пролом так не починить — его либо отстраивают заново (abilityRebuild),
// либо заколачивают деревянными воротами. Ремонт кладки идёт в тот же
// счёт, что и отстройка: два раза за ход на обе руки, иначе дыры
// закрывались бы быстрее, чем их делают.
func (s *State) engineerRepair(side Side, u *Unit, at Point) error {
	if s.b.TileAt(at) == TileWall {
		return s.repairWall(side, u, at)
	}
	if t := s.unitAt(at); t != nil {
		return s.repairEngine(side, u, t)
	}
	b := s.buildingAt(at)
	if b == nil || b.Owner != side {
		return fmt.Errorf("%w: чинить нечего", ErrBadTarget)
	}
	if b.HP >= b.MaxHP {
		return fmt.Errorf("%w: постройка цела", ErrBadTarget)
	}
	if s.supplies[side] < EngineerFixCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= EngineerFixCost
	u.actedTurn = s.turnCounter
	b.HP = minInt(b.HP+EngineerRepair, b.MaxHP)
	return nil
}

// repairBridge — осада чинит мост у ворот. Руки те же, что кладут частокол и
// мантелеты: полевые инженеры. Чинится он как любая постройка — по шесть
// прочности за действие, — так что сбитый требушетом мост возвращают не
// одним ходом, и всё это время переправа у ворот не работает.
func (s *State) repairBridge(side Side, u *Unit, at Point) error {
	if !s.canSap(u) {
		return fmt.Errorf("%w: мост чинят полевые инженеры осады", ErrBadTarget)
	}
	b := s.buildingAt(at)
	if b == nil || b.Kind != BuildBridge {
		return fmt.Errorf("%w: чинить нечего", ErrBadTarget)
	}
	if b.HP >= b.MaxHP {
		return fmt.Errorf("%w: мост цел", ErrBadTarget)
	}
	if s.supplies[side] < EngineerFixCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= EngineerFixCost
	u.actedTurn = s.turnCounter
	b.HP = minInt(b.HP+EngineerRepair, b.MaxHP)
	return nil
}

// wallRepairAmount — на сколько инженеры поднимают сегмент за один раз:
// пятая часть его полной прочности, но не меньше одного.
func wallRepairAmount(seg *WallSegment) int {
	if n := seg.MaxHP / EngineerWallShare; n > 0 {
		return n
	}
	return 1
}

func (s *State) repairWall(side Side, u *Unit, at Point) error {
	if side != SideDefender {
		return fmt.Errorf("%w: стену чинит замок", ErrBadTarget)
	}
	seg := s.segment(at)
	if seg == nil {
		return ErrBadTarget
	}
	if seg.Breached() {
		return fmt.Errorf("%w: пролом не латают — его отстраивают заново за %d припасов или заколачивают воротами",
			ErrBadTarget, s.rebuildCost(side))
	}
	if seg.HP >= seg.MaxHP {
		return fmt.Errorf("%w: %s целы", ErrBadTarget, seg.Kind.ru())
	}
	if s.supplies[side] < EngineerWallCost {
		return fmt.Errorf("%w: нужно %d, есть %d", ErrNoSupplies, EngineerWallCost, s.supplies[side])
	}
	if s.RepairsLeft(side) <= 0 {
		return fmt.Errorf("%w: кладку правят не больше %d раз за ход", ErrBadTarget, MaxWallRepairs)
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= EngineerWallCost
	s.noteRepair(side)
	u.actedTurn = s.turnCounter
	seg.HP = minInt(seg.HP+wallRepairAmount(seg), seg.MaxHP)
	return nil
}

// repairEngine — инженеры правят свою осадную машину: четверть полной
// прочности за раз. Лекарь машин не трогает, так что это единственный
// способ вернуть в строй разбитую баллисту или требушет.
func (s *State) repairEngine(side Side, u *Unit, target *Unit) error {
	if target.Owner != side {
		return fmt.Errorf("%w: чинят только свои машины", ErrBadTarget)
	}
	if !target.Spec().Mechanism() {
		return fmt.Errorf("%w: живых лечит лекарь, а не инженеры", ErrBadTarget)
	}
	if target.HP >= target.MaxHP() {
		return fmt.Errorf("%w: %s цела", ErrBadTarget, target.Spec().Label)
	}
	if s.supplies[side] < EngineerFixCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[side] -= EngineerFixCost
	u.actedTurn = s.turnCounter
	fix := target.Spec().MaxHP / EngineerEngShare
	if fix < 1 {
		fix = 1
	}
	target.HP = minInt(target.HP+fix, target.MaxHP())
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- лекарь ---

// healTargets — кого этот лекарь может поставить на ноги прямо сейчас:
// соседние свои отряды, которым есть что лечить.
func (s *State) healTargets(u *Unit) []Point {
	if !u.Spec().Healer || u.actedTurn == s.turnCounter {
		return nil
	}
	var out []Point
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		t := s.unitAt(p)
		if t == nil || t.Owner != u.Owner || t == u {
			continue
		}
		if t.Spec().Mechanism() || t.HP >= t.MaxHP() {
			continue
		}
		out = append(out, p)
	}
	sortPoints(out)
	return out
}

// doHeal — лекарь подходит и перевязывает соседа. Одно лечение за ход и
// только по живым: осадные машины чинит не он.
func (s *State) doHeal(side Side, a Action) error {
	u, err := s.ownUnit(side, a.UnitID)
	if err != nil {
		return err
	}
	if !u.Spec().Healer {
		return fmt.Errorf("%w: лечить умеет только лекарь", ErrBadTarget)
	}
	if u.actedTurn == s.turnCounter {
		return ErrUnitSpent
	}
	if Chebyshev(u.Pos, a.To) != 1 {
		return fmt.Errorf("%w: лечить можно только на соседней клетке", ErrBadTarget)
	}
	target := s.unitAt(a.To)
	if target == nil || target.Owner != side {
		return fmt.Errorf("%w: лечат только своих", ErrBadTarget)
	}
	if target.Spec().Mechanism() {
		return fmt.Errorf("%w: машину лекарь не починит — это дело инженеров", ErrBadTarget)
	}
	spec := target.Spec()
	if target.HP >= target.MaxHP() {
		return fmt.Errorf("%w: %s цел", ErrBadTarget, spec.Label)
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	u.actedTurn = s.turnCounter
	target.HP = minInt(target.HP+HealAmount, target.MaxHP())
	return nil
}

// --- стройка во дворе ---

// courtBuild — что замок может построить у себя во дворе. Это и есть вся
// стратегия защитника на длинной осаде: доход, оборона или живучесть стены.
// Всё построенное катапульта достаёт поверх стены, так что вкладываться
// приходится с оглядкой.
type courtBuild struct {
	Kind BuildingKind
	Cost int
	Max  int // сколько таких может стоять одновременно
	Hint string
}

var courtBuilds = map[string]courtBuild{
	"barracks": {BuildBarracks, BarracksCost, MaxBarracks,
		"+" + itoa(BarracksSupply) + " припаса в ход, пока целы"},
	"arrowTower": {BuildArrowTower, ArrowTowerCost, MaxArrowTower,
		"+" + itoa(TowerRangeBonus) + " дальности своим стрелкам в " + itoa(ArrowTowerRange) + " клетках"},
	"forge": {BuildForge, ForgeCost, MaxForge,
		"отстройка пролома дешевле на " + itoa(ForgeRebuildCut) + " припасов"},
	"warehouse": {BuildWarehouse, WarehouseCost, MaxWarehouse,
		"потолок обоза +" + itoa(WarehouseCap) + " припасов"},
}

// CourtBuildOrder — порядок в интерфейсе: карты в Go не упорядочены.
var CourtBuildOrder = []string{"barracks", "arrowTower", "forge", "warehouse"}

// courtBuildable — свободная ли клетка двора под стройку. Вокруг короля
// оставляем воздух: иначе замок замуровал бы его постройками и выиграл
// осаду, ни разу не подравшись.
func (s *State) courtBuildable(p Point) bool {
	if s.b.TileAt(p) != TileCourtyard {
		return false
	}
	if k := s.King(SideDefender); k != nil && Chebyshev(p, k.Pos) <= KingClearance {
		return false
	}
	return s.unitAt(p) == nil && s.buildingAt(p) == nil
}

// courtSpots — весь свободный двор: клиент подсвечивает его при стройке.
func (s *State) courtSpots() []Point {
	var out []Point
	for _, p := range s.b.InsideTiles() {
		if s.courtBuildable(p) {
			out = append(out, p)
		}
	}
	sortPoints(out)
	return out
}

func (s *State) doConstruct(side Side, a Action) error {
	if side != SideDefender {
		return fmt.Errorf("%w: строить во дворе может только замок", ErrBadTarget)
	}
	cb, ok := courtBuilds[a.Build]
	if !ok {
		return fmt.Errorf("%w: во дворе такого не строят: %q", ErrBadTarget, a.Build)
	}
	if !s.courtBuildable(a.To) {
		return fmt.Errorf("%w: нужна свободная клетка двора не вплотную к королю", ErrBadTarget)
	}
	if n := s.countBuildings(side, cb.Kind); n >= cb.Max {
		return fmt.Errorf("%w: %s больше %d не держат", ErrBadTarget, cb.Kind.Label(), cb.Max)
	}
	if s.supplies[side] < cb.Cost {
		return ErrNoSupplies
	}
	if err := s.spendAP(ConstructAPCost); err != nil {
		return err
	}
	s.supplies[side] -= cb.Cost
	hp := buildingHP(cb.Kind)
	s.Buildings = append(s.Buildings, &Building{
		ID: s.nextBuildingID, Kind: cb.Kind, Owner: side, Pos: a.To, HP: hp, MaxHP: hp,
	})
	s.nextBuildingID++
	return nil
}

// --- способности защитника ---

func (s *State) doAbility(side Side, a Action) error {
	// Воодушевление доступно всем троим: поднять войска перед решающим днём
	// может и замок, и любой из осаждающих.
	if a.Ability == "rally" {
		return s.abilityRally(side)
	}
	if side != SideDefender {
		return fmt.Errorf("%w: это умения защитника", ErrBadTarget)
	}
	// Ополчение созывают по всему замку, а не у конкретного сегмента, —
	// поэтому разбираем его до поиска сегмента стены.
	if a.Ability == "militia" {
		return s.abilityMilitia()
	}
	seg := s.segmentOf(a)
	if seg == nil {
		return fmt.Errorf("%w: нет такого сегмента стены", ErrBadTarget)
	}
	switch a.Ability {
	case "pitch":
		return s.abilityPitch(seg)
	case "rebuild":
		return s.abilityRebuild(seg)
	case "gate":
		return s.abilityGate(seg)
	}
	return fmt.Errorf("%w: неизвестное умение %q", ErrBadTarget, a.Ability)
}

// abilityGate — поднять или опустить решётку. Одно очко действия, никаких
// припасов: механизм, а не стройка.
//
// Открытые ворота — проход **для всех**, поэтому вылазка стала стоить
// дорого: пока отряд гуляет по полю, в обороне стоит дыра, и осаждающий
// видит её так же хорошо, как вы сами. Закрыть за собой створку — ещё одно
// действие, и ход на это надо планировать заранее.
func (s *State) abilityGate(seg *WallSegment) error {
	if !seg.Kind.IsGate() {
		return fmt.Errorf("%w: решётка есть только у ворот", ErrBadTarget)
	}
	if seg.Breached() {
		return fmt.Errorf("%w: ворота выбиты — поднимать нечего", ErrBadTarget)
	}
	// Опустить решётку на чужую голову нельзя: сначала выбейте отряд из
	// проёма. Поднять её всегда можно: пока она опущена, в проёме никого нет.
	if seg.Open && s.unitAt(seg.Pos) != nil {
		return fmt.Errorf("%w: в проёме стоит отряд — решётку не опустить", ErrBadTarget)
	}
	if err := s.spendAP(GateAPCost); err != nil {
		return err
	}
	seg.Open = !seg.Open
	return nil
}

// rebuildCost — во что обходится отстройка пролома. Целая кузница сбивает
// цену: это вся её польза, зато ощутимая — почти треть.
func (s *State) rebuildCost(side Side) int {
	cost := RebuildCost
	if s.forgeBonus(side) {
		cost -= ForgeRebuildCut
	}
	if cost < 1 {
		cost = 1
	}
	return cost
}

// abilityRally — воодушевление: до конца хода все свои отряды бьют вдвое
// сильнее. Стоит всего накопленного, поэтому это не «умение на каждый день»,
// а решение разом потратить казну на один штурм.
func (s *State) abilityRally(side Side) error {
	if s.Rallied(side) {
		return fmt.Errorf("%w: войска уже воодушевлены в этом ходу", ErrBadTarget)
	}
	if s.supplies[side] < RallyCost {
		return fmt.Errorf("%w: нужно %d, есть %d", ErrNoSupplies, RallyCost, s.supplies[side])
	}
	if err := s.spendAP(RallyAP); err != nil {
		return err
	}
	s.supplies[side] -= RallyCost
	s.rallyTurn[side] = s.turnCounter
	return nil
}

// PitchTile — куда выливается смола из этого столбца.
//
// Со стены льют **вниз, под стену**, а не себе под ноги в кладку: цель —
// ряд перед стеной, тот самый, где толпятся штурмующие. Линия сплошная, от
// края стены до края: ворота из неё не выпадают, лить в проём незачем —
// накрытие 3×3 из-под ворот достаёт и тех, кто уже в них.
func PitchTile(col int) Point {
	return Point{col, WallRow + 1}
}

// PitchTiles — все клетки, куда замок может плеснуть смолой на классической
// карте. Клиент берёт их из меты, где они уже от нужной доски.
func PitchTiles() []Point { return Classic.PitchTiles() }

// militiaSpots — где строится ополчение: свободные клетки замка, ближайшие
// к королю. Горожане собираются вокруг него, а не там, где удобно игроку, —
// и это значит, что король сам задаёт, куда придёт подкрепление.
func (s *State) militiaSpots(n int) []Point {
	var out []Point
	for _, p := range s.b.InsideTiles() {
		if s.unitAt(p) != nil || s.buildingAt(p) != nil {
			continue
		}
		out = append(out, p)
	}
	sortPoints(out)
	if king := s.King(SideDefender); king != nil {
		sort.SliceStable(out, func(i, j int) bool {
			return Chebyshev(out[i], king.Pos) < Chebyshev(out[j], king.Pos)
		})
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// MilitiaReady — можно ли созвать ополчение прямо сейчас. Нужно клиенту:
// кнопка гаснет раньше, чем игрок получит отказ.
func (s *State) MilitiaReady() bool {
	return s.Turn == SideDefender && s.Phase == PhasePlay && s.militiaBlock() == ""
}

// militiaBlock — почему ополчение сейчас не созвать; пусто — можно. Условий
// пять, и панели нужно знать, какое из них не выполнено: «уже созывали» и
// «не хватает припасов» лечатся по-разному.
func (s *State) militiaBlock() string {
	switch {
	case s.militiaTurn == s.turnCounter:
		return WhyUsed
	case s.militiaRoom() <= 0:
		return WhyMax
	case len(s.militiaSpots(1)) == 0:
		return WhyRoom
	case s.AP < MilitiaAP:
		return WhyAP
	case s.supplies[SideDefender] < MilitiaCost:
		return WhySupplies
	}
	return ""
}

// militiaRoom — сколько горожан ещё поместится в предел ополчения. Призыв
// добирает до потолка: если двое уже стоят, выйдет только третий.
func (s *State) militiaRoom() int {
	room := MaxMilitia - s.countUnits(SideDefender, UnitMilitia)
	if room > MilitiaCount {
		room = MilitiaCount
	}
	if room < 0 {
		room = 0
	}
	return room
}

// abilityMilitia — поднять горожан. Трое разом за одно действие, и в общий
// предел армии они не входят: за стеной и так живут люди, их не нанимают, а
// вооружают. Раз в ход — иначе кнопка заменила бы собой весь наём.
func (s *State) abilityMilitia() error {
	if s.militiaTurn == s.turnCounter {
		return fmt.Errorf("%w: ополчение уже созывали в этом ходу", ErrBadTarget)
	}
	room := s.militiaRoom()
	if room == 0 {
		return fmt.Errorf("%w: ополченцев на поле уже %d — больше не поднять", ErrBadTarget, MaxMilitia)
	}
	if s.supplies[SideDefender] < MilitiaCost {
		return fmt.Errorf("%w: нужно %d, есть %d", ErrNoSupplies, MilitiaCost, s.supplies[SideDefender])
	}
	spots := s.militiaSpots(room)
	if len(spots) == 0 {
		return fmt.Errorf("%w: в замке негде построиться", ErrBadTarget)
	}
	if err := s.spendAP(MilitiaAP); err != nil {
		return err
	}
	s.supplies[SideDefender] -= MilitiaCost
	s.militiaTurn = s.turnCounter
	for _, p := range spots {
		u := s.spawn(SideDefender, UnitMilitia, p)
		u.movedTurn, u.actedTurn = s.turnCounter, s.turnCounter // в день призыва не воюют
	}
	return nil
}

func (s *State) abilityPitch(seg *WallSegment) error {
	if s.PitchesLeft(SideDefender) <= 0 {
		return fmt.Errorf("%w: смолу льют не больше %d раз за ход", ErrBadTarget, MaxPitchPerTurn)
	}
	if s.supplies[SideDefender] < PitchCost {
		return ErrNoSupplies
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[SideDefender] -= PitchCost
	s.notePitch(SideDefender)
	centre := s.pitchTile(seg)
	for _, u := range append([]*Unit{}, s.Units...) {
		if !u.Owner.IsAttacker() {
			continue
		}
		// Смола достаёт только тех, кто снаружи: внутрь замка она не затекает.
		if s.b.IsInsideCastle(u.Pos) || Chebyshev(u.Pos, centre) > 1 {
			continue
		}
		s.hurt(u, PitchDamage)
		if u.HP <= 0 {
			s.killUnit(SideDefender, u)
		}
	}
	return nil
}

// abilityRebuild — отстроить стену на месте пролома. Не заплатка на девять
// прочности, а новый сегмент: цена в 35 припасов — это неделя дохода замка,
// и тратится она разом. Повреждённую, но стоящую стену чинить нечем: пока
// сегмент держится, он держится.
func (s *State) abilityRebuild(seg *WallSegment) error {
	if !seg.Breached() {
		return fmt.Errorf("%w: отстраивают только пролом — целый сегмент не трогают", ErrBadTarget)
	}
	if s.unitAt(seg.Pos) != nil {
		return fmt.Errorf("%w: в проломе стоит отряд", ErrBadTarget)
	}
	cost := s.rebuildCost(SideDefender)
	if s.supplies[SideDefender] < cost {
		return fmt.Errorf("%w: нужно %d, есть %d", ErrNoSupplies, cost, s.supplies[SideDefender])
	}
	if s.RepairsLeft(SideDefender) <= 0 {
		return fmt.Errorf("%w: стену отстраивают не больше %d раз за ход", ErrBadTarget, MaxWallRepairs)
	}
	if err := s.spendAP(1); err != nil {
		return err
	}
	s.supplies[SideDefender] -= cost
	s.noteRepair(SideDefender)
	// Поднимается то, что стояло здесь в начале осады: деревянная створка,
	// которой пролом заколачивали до того, отстройкой не воспроизводится —
	// за тридцать пять припасов замок получает кладку.
	seg.Kind, seg.MaxHP = s.b.originalSegment(seg.Pos)
	seg.HP = seg.MaxHP
	seg.Open = false
	return nil
}
