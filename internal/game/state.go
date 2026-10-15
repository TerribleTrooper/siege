package game

import (
	"errors"
	"fmt"
)

var (
	ErrNotYourTurn  = errors.New("не ваш ход")
	ErrNoAP         = errors.New("не осталось очков действия")
	ErrNoSupplies   = errors.New("не хватает припасов")
	ErrNoSilver     = errors.New("не хватает серебра")
	ErrBadTarget    = errors.New("недопустимая цель")
	ErrUnitNotFound = errors.New("юнит не найден")
	ErrUnitSpent    = errors.New("юнит уже действовал в этом ходу")
	ErrPhase        = errors.New("партия уже окончена")
)

// Phase — идёт ли партия. Партия — это одна осада: король пал или замок
// выстоял — она окончена, и следующей за ней нет.
type Phase uint8

const (
	PhasePlay Phase = iota
	PhaseOver
)

var phaseNames = map[Phase]string{PhasePlay: "play", PhaseOver: "over"}

func (p Phase) String() string { return phaseNames[p] }

// Unit — фигура на поле.
type Unit struct {
	ID    int      `json:"id"`
	Owner Side     `json:"-"`
	Type  UnitType `json:"-"`
	HP    int      `json:"hp"`
	Pos   Point    `json:"pos"`

	movedTurn int // номер хода, в котором юнит двигался
	actedTurn int // ... и атаковал/закладывал заряд

	// Charges — сколько зарядов осталось у подрывника. Один заряд обрушивает
	// сегмент целиком, поэтому больше одного ему и не дают.
	Charges int `json:"charges,omitempty"`

	// Carrier — ID осадной башни, внутри которой едет отряд (0 — своими
	// ногами). Пассажира нет на карте: он не занимает клетку, не перекрывает
	// дорогу, его нельзя ни увидеть, ни обстрелять — он разделяет судьбу
	// башни, пока не сойдёт по трапу.
	Carrier int `json:"-"`

	// Ramp — опущен ли штурмовой трап. Только у осадной башни; при первом
	// же её шаге трап складывается обратно.
	Ramp bool `json:"ramp,omitempty"`

	// Kills — сколько чужих отрядов добил этот. Отсюда растут звёзды
	// ветерана: каждая даёт +1 к урону и +1 к прочности. Счёт упирается в
	// VeteranMaxStars — выше отряд не растёт, сколько бы ни убивал.
	Kills int `json:"kills,omitempty"`

	// spotted — кто из сторон знает про этот отряд, пока он сидит в кустах.
	// Вне кустов поле не используется: там всё решает обычный туман.
	spotted [4]bool
}

// reveal — открыть отряд стороне. Осаждающие делят разведку, поэтому
// раскрытие одному открывает его и союзнику по осаде.
func (u *Unit) reveal(to Side) {
	u.spotted[to] = true
	if to.IsAttacker() {
		u.spotted[SideAttackerA] = true
		u.spotted[SideAttackerB] = true
	}
}

// revealAll — отряд обнаружен всеми: так бывает после удара по нему и после
// его собственной атаки из зарослей.
func (u *Unit) revealAll() {
	for s := range u.spotted {
		u.spotted[s] = true
	}
}

// concealed — прячется ли отряд в кустах от этой стороны.
func (s *State) concealed(u *Unit, from Side) bool {
	return InBush(u.Pos) && !u.spotted[from] && areEnemies(u.Owner, from)
}

// Lurking — сидит ли отряд в засаде хоть от кого-нибудь из врагов. Нужно
// владельцу: по этому признаку клиент показывает, что отряд ещё не раскрыт.
func (s *State) Lurking(u *Unit) bool {
	if !InBush(u.Pos) {
		return false
	}
	for _, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
		if s.concealed(u, side) {
			return true
		}
	}
	return false
}

// refreshCover возвращает в засаду тех, кого противник потерял из виду.
//
// Поле spotted — это память противника о том, где вы, а не клеймо на всю
// партию. Раньше оно работало защёлкой в одну сторону: отряд, который хоть
// раз засветился, оставался видимым в кустах навсегда, даже когда вокруг не
// было ни одного вражеского глаза, — и вернуться в засаду можно было только
// перебежкой в соседний куст. Теперь проще: не видят твою клетку — значит,
// потеряли, и заросли снова работают.
//
// Снимать скрытность этот пересчёт не умеет: он только возвращает её. Выйти
// из засады по-прежнему можно лишь своими действиями — ударить, попасть под
// удар, выйти на открытое место. Поэтому смотреть на куст в упор всё так же
// бесполезно: враг рядом видит клетку, но сидящего в ней — нет.
func (s *State) refreshCover() {
	vis := map[Side]map[Point]bool{}
	for _, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
		vis[side] = s.VisibleFor(side)
	}
	for _, u := range s.Units {
		if !InBush(u.Pos) || u.Carrier != 0 {
			continue
		}
		for _, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
			if areEnemies(u.Owner, side) && !vis[side][u.Pos] {
				u.spotted[side] = false
			}
		}
	}
}

// VisibleUnit — видит ли сторона этот отряд прямо сейчас. Здесь сходятся оба
// механизма сокрытия: туман войны и засада в кустах.
func (s *State) VisibleUnit(u *Unit, side Side) bool {
	if u.Owner == side || !areEnemies(u.Owner, side) {
		return true // свои и союзник по осаде видны всегда
	}
	if !s.VisibleFor(side)[u.Pos] {
		return false
	}
	return !s.concealed(u, side)
}

// hurt наносит урон отряду. Единственная точка, где у отряда убывает
// здоровье: что-то прилетело — значит, в зарослях его больше не спрячешь.
func (s *State) hurt(u *Unit, dmg int) {
	u.HP -= dmg
	if dmg > 0 {
		u.revealAll()
	}
}

func (u *Unit) Spec() UnitSpec { return Spec(u.Type) }

// Stars — сколько звёзд ветерана заслужил отряд. Звезда за каждый добитый
// чужой отряд, но не больше трёх: дальше опыт ничего не добавляет.
func (u *Unit) Stars() int {
	if u.Kills > VeteranMaxStars {
		return VeteranMaxStars
	}
	return u.Kills
}

// MaxHP — полная прочность **этого** отряда: по типу плюс звёзды. Везде, где
// раньше брали Spec().MaxHP у живого юнита, теперь нужен этот метод — иначе
// ветеран не долечивается до своего настоящего предела.
func (u *Unit) MaxHP() int { return u.Spec().MaxHP + u.Stars()*VeteranHP }

// promote — отряд добил чужой и получает звезду. Вместе со звездой растёт и
// предел прочности, поэтому отряду сразу добавляется то же здоровье: иначе
// награда за убийство выглядела бы как ранение.
func (s *State) promote(u *Unit) {
	if u == nil || u.Stars() >= VeteranMaxStars {
		return
	}
	u.Kills++
	u.HP += VeteranHP
}

type Seat struct {
	Name  string `json:"name"`
	Score int    `json:"score"` // очки за осаду: начисляются, когда она кончилась
}

// Result — итог осады, он же итог партии.
type Result struct {
	Captured   bool   `json:"captured"`
	CapturedBy string `json:"capturedBy"`
	Days       int    `json:"days"`
	Summary    string `json:"summary"`
}

// State — полное состояние партии. Вся логика правил живёт здесь и не знает
// ничего про HTTP, комнаты и сеть.
type State struct {
	Phase Phase   `json:"phase"`
	Day   int     `json:"day"`
	Turn  Side    `json:"-"`
	AP    int     `json:"ap"`
	Seats [3]Seat `json:"seats"`

	Wall      []WallSegment `json:"wall"`
	Units     []*Unit       `json:"-"`
	Buildings []*Building   `json:"-"`
	Traps     []Trap        `json:"-"`

	supplies [4]int
	silver   [4]int // добывается только на руднике, тратится только на элиту
	// статистика для подсчёта очков
	structDamage  [4]int
	breached      [4]bool
	enginesKilled int

	// rallyTurn — ход, в котором сторона воодушевила войска. Сравнивается с
	// turnCounter, поэтому эффект гаснет сам со сменой хода.
	rallyTurn [4]int

	// Счётчики «сколько раз за этот ход». Привязаны к turnCounter, поэтому
	// обнулять их отдельно не нужно: сменился ход — счёт начался заново.
	// militiaTurn — ход, в котором замок поднимал ополчение. Как и
	// воодушевление, привязано к turnCounter: сбрасывать не нужно.
	militiaTurn int

	fixTurn    [4]int // ремонт стены: умение замка и инженеры считаются вместе
	fixCount   [4]int
	hireTurn   [4]int // наём отрядов
	hireCount  [4]int
	pitchTurn  [4]int // кипящая смола
	pitchCount [4]int

	turnCounter    int // монотонный счётчик ходов, для флагов "юнит уже действовал"
	nextUnitID     int
	nextBuildingID int

	Result *Result `json:"result"` // nil, пока осада идёт
}

// --- ров ---

// moatOpen — осталась ли на этой клетке открытая вода. Настил из вязанок
// перестаёт быть препятствием: по нему к стене подходят и машины. Настил —
// обычная постройка с прочностью, и когда его разбивают, вода возвращается
// сама собой: отдельного флага у рва нет.
func (s *State) moatOpen(p Point) bool {
	if TileAt(p) != TileMoat {
		return false
	}
	b := s.buildingAt(p)
	return b == nil || !b.Kind.Deck()
}

// fillMoatAt — забросать клетку рва вязанками. Возвращает false, если
// засыпать нечего.
func (s *State) fillMoatAt(p Point, owner Side) bool {
	if !s.moatOpen(p) {
		return false
	}
	s.Buildings = append(s.Buildings, &Building{
		ID: s.nextBuildingID, Kind: BuildFascine, Owner: owner, Pos: p,
		HP: FascineHP, MaxHP: FascineHP,
	})
	s.nextBuildingID++
	return true
}

// MoatOpenTiles — какие клетки рва ещё не засыпаны. Уходит в снимок: ров на
// виду у всех, скрывать в нём нечего.
func (s *State) MoatOpenTiles() []Point {
	var out []Point
	for _, p := range MoatTiles() {
		if s.moatOpen(p) {
			out = append(out, p)
		}
	}
	return out
}

// moveCost — во сколько обходится вход в клетку. Открытой воды среди таких
// клеток нет: в ров не входит никто, см. tilePassable.
func (s *State) moveCost(p Point) int {
	return MoveCost(p)
}

// --- припасы и потолок обоза ---

// SupplyCap — сколько припасов сторона может держать в обозе. Склады
// поднимают потолок; всё, что выше, пропадает в тот же ход, когда пришло.
func (s *State) SupplyCap(side Side) int {
	return SupplyCap + s.countBuildings(side, BuildWarehouse)*WarehouseCap
}

// IncomePart — одна статья дохода: откуда и сколько припасов придёт в начале
// хода. Панель показывает статьи рядом с итогом, иначе «+8 в ход» — число с
// потолка, и непонятно, отчего оно упало после потери казарм.
type IncomePart struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
}

// Income — что сторона получит в начале своего следующего хода, если расклад
// на поле до тех пор не изменится: припасы по статьям и серебро с рудника.
// endTurn начисляет доход этой же функцией, поэтому прогноз в панели и
// настоящее начисление разойтись не могут.
func (s *State) Income(side Side) (parts []IncomePart, silver int) {
	if side == SideDefender {
		parts = append(parts, IncomePart{Label: "обоз", Amount: DefenderSupplyGet})
		// Казармы кормят замок, пока целы: их выгодно разбить катапультой.
		if n := s.countBuildings(side, BuildBarracks); n > 0 {
			parts = append(parts, IncomePart{Label: "казармы", Amount: n * BarracksSupply})
		}
	} else {
		parts = append(parts, IncomePart{Label: "обоз", Amount: AttackerSupplyGet})
	}
	// Пашни по флангам: обоз подвозит припасы тому, кто на них стоит. Это
	// вторая экономика помимо рудника — и единственная, за которую не надо
	// драться в центре карты.
	if n := s.FarmsHeldBy(side); n > 0 {
		parts = append(parts, IncomePart{Label: "пашни", Amount: n * FarmSupply})
	}
	// Наблюдательные пункты — ещё и перевалочные пункты обоза: за каждый
	// удержанный капают припасы. Поэтому их и занимают, а не обходят.
	if n := s.towersHeld(side); n > 0 {
		parts = append(parts, IncomePart{Label: "вышки", Amount: n * TowerSupply})
	}
	// Рудник платит тому, кто стоит на нём в начале своего хода. Серебро
	// потолка не знает: его и добыть-то можно только отрядом на клетке.
	return parts, s.MineHeldBy(side) * SilverPerTile
}

// IncomeTotal — сумма статей: столько припасов придёт, если обоз не упрётся
// в потолок.
func IncomeTotal(parts []IncomePart) int {
	n := 0
	for _, p := range parts {
		n += p.Amount
	}
	return n
}

// addSupplies — единственная точка, где припасы прибывают. Сверх потолка
// обоз не берёт: излишек растащен, и копить его негде.
func (s *State) addSupplies(side Side, n int) {
	if n <= 0 {
		return
	}
	cap := s.SupplyCap(side)
	s.supplies[side] += n
	if s.supplies[side] > cap {
		s.supplies[side] = cap
	}
}

// New создаёт партию — одну осаду. Имена — по местам (seat 0..2).
//
// Случайности в партии нет вовсе: карта, рельеф и стартовые постройки
// одинаковы каждый раз. Вышки на поле больше не разбрасываются — их ставят
// сами игроки, и зерно для этого не нужно.
func New(names [3]string) *State {
	s := &State{Seats: [3]Seat{{Name: names[0]}, {Name: names[1]}, {Name: names[2]}}}
	s.setup()
	return s
}

// seatSides — роль каждого места. Роли закреплены за местами на всю партию:
// первое держит замок, второе и третье осаждают.
var seatSides = [3]Side{SideDefender, SideAttackerA, SideAttackerB}

// SeatOf возвращает место игрока, играющего за данную сторону.
func (s *State) SeatOf(side Side) int {
	for seat, sd := range seatSides {
		if sd == side {
			return seat
		}
	}
	return -1
}

// SideOf возвращает роль игрока по его месту.
func (s *State) SideOf(seat int) Side {
	if seat < 0 || seat >= len(seatSides) {
		return SideNeutral
	}
	return seatSides[seat]
}

func (s *State) Supplies(side Side) int { return s.supplies[side] }

// Silver — серебро стороны: валюта элитных отрядов.
func (s *State) Silver(side Side) int { return s.silver[side] }

// MineHeldBy — сколько клеток рудника удерживает сторона.
func (s *State) MineHeldBy(side Side) int {
	held := 0
	for _, p := range MineTiles() {
		if u := s.unitAt(p); u != nil && u.Owner == side {
			held++
		}
	}
	return held
}

// MineOwners — кто стоит на клетках рудника. Видно всем: рудник на виду.
func (s *State) MineOwners() map[Point]Side {
	out := map[Point]Side{}
	for _, p := range MineTiles() {
		if u := s.unitAt(p); u != nil {
			out[p] = u.Owner
		}
	}
	return out
}

// setup расставляет осаду к первому дню. Зовётся один раз, на свежем
// состоянии: счётчики и статистика очков и так начинаются с нуля.
func (s *State) setup() {
	s.Phase = PhasePlay
	s.Day = 1
	s.Turn = SideDefender
	s.Wall = newWall()
	s.nextUnitID = 1
	s.nextBuildingID = 1
	s.Buildings = startingBuildings(func() int {
		id := s.nextBuildingID
		s.nextBuildingID++
		return id
	})
	s.turnCounter = 1
	s.supplies[SideDefender] = DefenderSupply0
	s.supplies[SideAttackerA] = AttackerSupply0
	s.supplies[SideAttackerB] = AttackerSupply0
	s.AP = APPerTurn

	// Король — приз осады. Он не нанимается и не воскресает: убили — партия
	// кончилась. Начинает в середине двора, дальше замок водит его сам.
	king := s.spawn(SideDefender, UnitKing, ThroneTile())

	// И при нём стража: двое храмовников по бокам. Они не нанимаются и не
	// покупаются — появляются вместе с королём и за стену не выходят.
	// Оставлять приз вовсе без охраны нельзя: иначе осада достаётся первому
	// же отряду, добежавшему до пролома.
	for _, dx := range []int{-1, 1} {
		if p := king.Pos.Add(dx, 0); p.InBounds() && TileAt(p) == TileCourtyard && s.unitAt(p) == nil {
			s.spawn(SideDefender, UnitTemplar, p)
		}
	}

	// Защитнику дают стартовый гарнизон, чтобы первый день не был пустым.
	s.spawn(SideDefender, UnitGarrison, Point{GateColLeft, WalkRow})
	s.spawn(SideDefender, UnitEngineer, Point{GateColRight, WalkRow})
	s.spawn(SideDefender, UnitScout, Point{WallFirstCol + 3, WalkRow})
	s.spawn(SideDefender, UnitScout, Point{WallLastCol - 3, WalkRow})
}

// Rallied — действует ли сейчас воодушевление этой стороны. Привязано к
// номеру хода, так что сбрасывать его отдельно не нужно.
func (s *State) Rallied(side Side) bool {
	return s.rallyTurn[side] == s.turnCounter && s.turnCounter > 0
}

// unitDamage — урон отряда с учётом звёзд, воодушевления и позиции.
// Единственная точка, где базовый урон превращается в фактический: иначе
// множители пришлось бы помнить в пяти местах разом.
//
// Порядок важен: сначала к базовому урону прибавляются звёзды ветерана и
// башня, и только потом всё это удваивает воодушевление. Опыт и позиция —
// часть силы отряда, а не надбавка поверх неё.
func (s *State) unitDamage(u *Unit) int {
	return s.damageFrom(u, u.Spec().Damage)
}

// damageFrom — та же надбавка, но от любого базового числа: у катапульты
// урон по кладке свой, а звёзды, башня и воодушевление считаются от него
// ровно так же, как от боевого.
func (s *State) damageFrom(u *Unit, base int) int {
	spec := u.Spec()
	if base <= 0 {
		return base // машины без боевого применения звёздами не улучшить
	}
	dmg := base + u.Stars()*VeteranDamage
	// Стрелок, засевший в башне, бьёт тяжелее: сверху видно, куда целиться,
	// и стрела идёт сверху вниз.
	if spec.Range > 1 && s.inTower(u.Pos) {
		dmg += TowerDamageBonus
	}
	if s.Rallied(u.Owner) {
		dmg *= RallyFactor
	}
	return dmg
}

// wallDamage — сколько отряд снимает за удар с кладки, ворот или постройки.
// Одно место на все ветки атаки: иначе осадный урон пришлось бы помнить в
// каждой из них, и однажды его бы там забыли.
func (s *State) wallDamage(u *Unit) int {
	spec := u.Spec()
	if spec.SiegeDamage > 0 {
		return s.damageFrom(u, spec.SiegeDamage)
	}
	dmg := s.unitDamage(u)
	if spec.VsWall > 0 {
		dmg *= spec.VsWall
	}
	return dmg
}

// shieldFor — мантелет, который примет стрелу вместо этой цели.
//
// Щит работает только против стрельбы: он стоит на клетке между стрелком и
// тем, кто идёт следом, и перехватывает то, что летит по прямой. В
// рукопашной его обходят, а навесом (катапульта, требушет, смола) бьют
// поверх — от камня сверху доска на раме не спасает.
//
// Прикрывает он своих и союзника по осаде; врага, прижавшегося к чужому
// щиту с другой стороны, мантелет не защищает.
func (s *State) shieldFor(target *Unit, from Point) *Unit {
	if Chebyshev(from, target.Pos) <= 1 {
		return nil
	}
	cover := target.Pos.Add(sign(from.X-target.Pos.X), sign(from.Y-target.Pos.Y))
	m := s.unitAt(cover)
	if m == nil || m.Type != UnitMantlet || areEnemies(m.Owner, target.Owner) {
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

// unitVision — обзор отряда. Стрелку в башне видно на клетку дальше: он
// смотрит с помоста, а не из травы.
func (s *State) unitVision(u *Unit) int {
	v := u.Spec().Vision
	if u.Spec().Range > 1 && s.inTower(u.Pos) {
		v += TowerVisionBonus
	}
	return v
}

func (s *State) spawn(side Side, t UnitType, at Point) *Unit {
	u := &Unit{ID: s.nextUnitID, Owner: side, Type: t, HP: Spec(t).MaxHP, Pos: at, Charges: Spec(t).Charges}
	// Отряд, появившийся на открытом месте, прятать не от кого.
	if !InBush(at) {
		u.revealAll()
	}
	s.nextUnitID++
	s.Units = append(s.Units, u)
	return u
}

func (s *State) unitAt(p Point) *Unit {
	for _, u := range s.Units {
		// Пассажир осадной башни стоит на её клетке, но клетку занимает
		// башня, а не он.
		if u.Pos == p && u.Carrier == 0 {
			return u
		}
	}
	return nil
}

// cargoOf — кто едет внутри этой машины, в порядке посадки.
func (s *State) cargoOf(carrier *Unit) []*Unit {
	var out []*Unit
	for _, u := range s.Units {
		if u.Carrier == carrier.ID {
			out = append(out, u)
		}
	}
	return out
}

// wreckDamage — сколько получает пассажир, когда машину под ним разбили:
// половину полного здоровья, с округлением вверх. Считается от полного, а
// не от оставшегося: раненому из обломков выбраться тем более нечем.
func wreckDamage(u *Unit) int {
	return (u.MaxHP() + WreckShare - 1) / WreckShare
}

// ArmySize — сколько живых отрядов у стороны прямо сейчас. Ополчение в счёт
// не идёт: горожан не нанимают, их вооружают, и предел армии к ним не
// относится.
func (s *State) ArmySize(side Side) int {
	n := 0
	for _, u := range s.Units {
		if u.Owner == side && !u.Spec().Levy {
			n++
		}
	}
	return n
}

// --- пределы «сколько раз за ход» ---
//
// turnCounter монотонно растёт, поэтому счётчик достаточно помечать номером
// хода: как только ход сменился, старая метка перестаёт совпадать, и счёт
// начинается с нуля сам. Сбрасывать ничего не надо.

func perTurnLeft(turn *int, count *int, now, limit int) int {
	if *turn != now {
		return limit
	}
	if left := limit - *count; left > 0 {
		return left
	}
	return 0
}

func perTurnUse(turn *int, count *int, now int) {
	if *turn != now {
		*turn, *count = now, 0
	}
	*count++
}

// RepairsLeft — сколько ремонтов стены стороне осталось в этом ходу. Умение
// замка и инженеры тратят один и тот же счёт.
func (s *State) RepairsLeft(side Side) int {
	return perTurnLeft(&s.fixTurn[side], &s.fixCount[side], s.turnCounter, MaxWallRepairs)
}

func (s *State) noteRepair(side Side) {
	perTurnUse(&s.fixTurn[side], &s.fixCount[side], s.turnCounter)
}

// HiresLeft — сколько отрядов сторона ещё может нанять в этом ходу.
func (s *State) HiresLeft(side Side) int {
	return perTurnLeft(&s.hireTurn[side], &s.hireCount[side], s.turnCounter, MaxDeploysPerTurn)
}

func (s *State) noteHire(side Side) {
	perTurnUse(&s.hireTurn[side], &s.hireCount[side], s.turnCounter)
}

// PitchesLeft — сколько раз сторона ещё может плеснуть смолой в этом ходу.
func (s *State) PitchesLeft(side Side) int {
	return perTurnLeft(&s.pitchTurn[side], &s.pitchCount[side], s.turnCounter, MaxPitchPerTurn)
}

func (s *State) notePitch(side Side) {
	perTurnUse(&s.pitchTurn[side], &s.pitchCount[side], s.turnCounter)
}

// FarmsHeldBy — сколько пашен занято отрядами этой стороны.
func (s *State) FarmsHeldBy(side Side) int {
	held := 0
	for _, p := range FarmTiles() {
		if u := s.unitAt(p); u != nil && u.Owner == side {
			held++
		}
	}
	return held
}

// King — король этой стороны, если он ещё жив.
func (s *State) King(side Side) *Unit {
	for _, u := range s.Units {
		if u.Owner == side && u.Spec().Royal {
			return u
		}
	}
	return nil
}

// ArmyCap — предел армии стороны. У замка он выше: он один против двоих.
func ArmyCap(side Side) int {
	if side == SideDefender {
		return MaxArmyDefender
	}
	return MaxArmyAttacker
}

// countUnits — сколько живых отрядов данного типа у стороны. По нему
// работает предел на число катапульт.
func (s *State) countUnits(side Side, t UnitType) int {
	n := 0
	for _, u := range s.Units {
		if u.Owner == side && u.Type == t {
			n++
		}
	}
	return n
}

func (s *State) unitByID(id int) *Unit {
	for _, u := range s.Units {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *State) segment(col int) *WallSegment {
	if col < WallFirstCol || col > WallLastCol {
		return nil
	}
	return &s.Wall[segmentIndex(col)]
}

func (s *State) removeUnit(u *Unit) {
	for i, x := range s.Units {
		if x == u {
			s.Units = append(s.Units[:i], s.Units[i+1:]...)
			return
		}
	}
}

// turnOrder — порядок хода внутри дня: сначала замок, потом осаждающие.
var turnOrder = [3]Side{SideDefender, SideAttackerA, SideAttackerB}

// SideAfter — кто ходит следом за side, с переходом через смену дня. Нужно
// комнате с ботами: на общем экране, пока ходит бот, сидит следующий по
// очереди человек.
func SideAfter(side Side) Side {
	for i, s := range turnOrder {
		if s == side {
			return turnOrder[(i+1)%len(turnOrder)]
		}
	}
	return SideDefender
}

func (s *State) endTurn() {
	// Неиспользованные очки действия уходят в припасы один к одному —
	// награда за планирование и способ копить на дорогие машины. Выше
	// потолка обоза они не поднимутся: без склада копить некуда.
	if s.AP > 0 {
		s.addSupplies(s.Turn, s.AP*APCarryPerPoint)
	}
	idx := 0
	for i, side := range turnOrder {
		if side == s.Turn {
			idx = i
		}
	}
	next := turnOrder[(idx+1)%len(turnOrder)]
	if idx == len(turnOrder)-1 {
		s.Day++
		if s.Day > SiegeDays {
			s.finish(nil)
			return
		}
		// Временные постройки (аванпосты) живут считанные дни.
		s.expireBuildings()
	}
	s.Turn = next
	s.turnCounter++
	s.AP = APPerTurn
	// Кого потеряли из виду за прошедший ход — тот снова в засаде.
	s.refreshCover()
	// Доход считает Income — та же функция, что рисует прогноз в панели.
	parts, silver := s.Income(next)
	s.addSupplies(next, IncomeTotal(parts))
	s.silver[next] += silver
}

// finish заканчивает партию и считает очки. captor != nil означает, что
// король убит.
func (s *State) finish(captor *Side) {
	res := Result{Days: s.Day}
	seatPoints := [3]int{}

	if captor != nil {
		res.Captured = true
		res.CapturedBy = captor.String()
		seatPoints[s.SeatOf(*captor)] += ScoreCapture

		rival := SideAttackerA
		if *captor == SideAttackerA {
			rival = SideAttackerB
		}
		switch {
		case s.breached[rival]:
			seatPoints[s.SeatOf(rival)] += ScoreRivalBreach
		case s.structDamage[rival] > 0:
			seatPoints[s.SeatOf(rival)] += ScoreRivalDamage
		}
		// Защитнику — утешительные очки за сожжённые машины.
		kills := s.enginesKilled
		if kills > ScoreDefenderMax {
			kills = ScoreDefenderMax
		}
		seatPoints[s.SeatOf(SideDefender)] += kills * ScoreDefenderKill
		res.Summary = fmt.Sprintf("Король убит на %d-й день: %s", s.Day, s.Seats[s.SeatOf(*captor)].Name)
	} else {
		seatPoints[s.SeatOf(SideDefender)] += ScoreDefenderHold
		for _, side := range []Side{SideAttackerA, SideAttackerB} {
			if s.breached[side] {
				seatPoints[s.SeatOf(side)] += ScoreBreachConsole
			}
		}
		res.Summary = fmt.Sprintf("Замок выстоял все %d дней: %s", SiegeDays, s.Seats[s.SeatOf(SideDefender)].Name)
	}

	for seat := 0; seat < 3; seat++ {
		s.Seats[seat].Score = seatPoints[seat]
	}
	// Кто победил — в ту же строку: отдельной хроники, куда его можно было
	// бы положить, нет.
	res.Summary += ". " + s.standingsText()

	s.Phase = PhaseOver
	s.Result = &res
}

func (s *State) standingsText() string {
	best, name := -1, ""
	tie := false
	for _, seat := range s.Seats {
		switch {
		case seat.Score > best:
			best, name, tie = seat.Score, seat.Name, false
		case seat.Score == best:
			tie = true
		}
	}
	if tie {
		return fmt.Sprintf("Ничья на %d очках", best)
	}
	return fmt.Sprintf("Побеждает %s с %d очками", name, best)
}
