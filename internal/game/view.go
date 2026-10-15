package game

// View — снимок состояния для одного конкретного игрока. Скрытая информация
// (засады, ловушки) вырезается здесь, а не на клиенте: иначе её можно подсмотреть
// в сетевом трафике.
type View struct {
	Phase     string         `json:"phase"`
	Day       int            `json:"day"`
	Days      int            `json:"days"`
	Turn      string         `json:"turn"`
	TurnName  string         `json:"turnName"`
	AP        int            `json:"ap"`
	APMax     int            `json:"apMax"`
	Seats     []SeatView     `json:"seats"`
	You       YouView        `json:"you"`
	Wall      []WallSegment  `json:"wall"`
	Units     []UnitView     `json:"units"`
	Buildings []BuildingView `json:"buildings"`
	Traps     []Point        `json:"traps,omitempty"` // только свои: чужие не видно
	Mine      []MineView     `json:"mine"`
	Farm      []MineView     `json:"farm"`
	Fog       []string       `json:"fog"` // построчно: '1' — видно, '0' — туман
	Buyable   []BuyView      `json:"buyable"`
	Result    *Result        `json:"result"` // итог партии: только когда она окончена

	// Moat — какие клетки рва ещё открыты. Засыпанные в список не попадают:
	// по ним уже ходят как по полю, и рисовать их канавой нельзя.
	Moat []Point `json:"moat"`
}

type SeatView struct {
	Seat  int    `json:"seat"`
	Name  string `json:"name"`
	Score int    `json:"score"`
	Side  string `json:"side"`
	Turn  bool   `json:"turn"`
}

type YouView struct {
	Seat     int     `json:"seat"`
	Side     string  `json:"side"`
	Supplies int     `json:"supplies"`
	Silver   int     `json:"silver"`
	Mine     int     `json:"mine"`  // сколько клеток рудника удерживаем
	Farms    int     `json:"farms"` // и сколько пашен
	MyTurn   bool    `json:"myTurn"`
	Zone     []Point `json:"zone"` // куда можно ставить новых юнитов

	// SupplyCap — потолок обоза: сверх него припасы не копятся. Клиент
	// показывает его рядом с самими припасами, иначе исчезающий излишек
	// выглядит ошибкой.
	SupplyCap int `json:"supplyCap"`

	// Income — сколько припасов придёт в начале следующего хода, IncomeParts
	// — из чего это число складывается, SilverIncome — серебро с рудника.
	// Прогноз «если ничего не изменится»: казармы могут разбить, с пашни и
	// рудника — согнать.
	Income       int          `json:"income"`
	IncomeParts  []IncomePart `json:"incomeParts"`
	SilverIncome int          `json:"silverIncome"`

	// Стройка во дворе: только у замка. Пустой список — строить негде.
	Court   []Point       `json:"court,omitempty"`
	Builds  []CourtOption `json:"courtBuilds,omitempty"`
	Towers  int           `json:"towers"`  // сколько наблюдательных пунктов за нами
	Army    int           `json:"army"`    // отрядов на поле
	ArmyMax int           `json:"armyMax"` // и сколько их можно держать
	Rally   bool          `json:"rally"`   // воодушевление действует прямо сейчас
	RallyOK bool          `json:"rallyOk"` // хватает припасов и очков на него

	// MilitiaOK — можно ли созвать ополчение прямо сейчас, а MilitiaWhy —
	// почему нельзя (см. Why*). Только у замка и только в его ход.
	MilitiaOK  bool   `json:"militiaOk"`
	MilitiaWhy string `json:"militiaWhy,omitempty"`
	Rebuild    int    `json:"rebuild"` // во сколько припасов обойдётся отстройка пролома

	// Наблюдательный пункт: цена у сторон разная, и сколько ещё можно.
	Watch    int `json:"watch"`
	WatchMax int `json:"watchMax"`
	WatchHad int `json:"watchHad"`

	// Пределы на этот ход: сколько ремонтов стены, наймов и вёдер смолы
	// ещё осталось.
	Fixes      int `json:"fixes"`
	FixesMax   int `json:"fixesMax"`
	Hires      int `json:"hires"`
	HiresMax   int `json:"hiresMax"`
	Pitches    int `json:"pitches"`
	PitchesMax int `json:"pitchesMax"`

	// King — где стоит свой король и сколько у него здоровья. Защитнику это
	// нужно постоянно: приз осады — единственная фигура, которую нельзя
	// потерять, и следить за ней приходится отдельно от всей армии.
	King *KingView `json:"king,omitempty"`
}

// KingView — короткая сводка о короле для панели.
type KingView struct {
	ID    int   `json:"id"`
	HP    int   `json:"hp"`
	MaxHP int   `json:"maxHp"`
	Pos   Point `json:"pos"`
}

// CourtOption — одна постройка, доступная замку во дворе.
type CourtOption struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Cost       int    `json:"cost"`
	AP         int    `json:"ap"` // цена в очках действия
	Hint       string `json:"hint"`
	Have       int    `json:"have"`
	Max        int    `json:"max"`
	Affordable bool   `json:"affordable"`
	Why        string `json:"why,omitempty"` // почему недоступна, см. Why*
}

// Почему позиция лавки сейчас недоступна. Клиент по коду пишет причину в
// строке и решает, рисовать ли полоску накопления: копить имеет смысл только
// валюту — предел армии ожиданием не лечится.
const (
	WhyMax      = "max"      // предел таких отрядов или построек
	WhyArmy     = "army"     // предел армии
	WhyHires    = "hires"    // наём на этот ход исчерпан
	WhyCourt    = "court"    // во дворе нет свободной клетки
	WhyUsed     = "used"     // умение уже применяли в этом ходу
	WhyRoom     = "room"     // негде поставить: вокруг короля всё занято
	WhyAP       = "ap"       // не хватает очков действия
	WhySilver   = "silver"   // не хватает серебра
	WhySupplies = "supplies" // не хватает припасов
)

// buyBlock — почему отряд сейчас не нанять; пусто — можно. Условия те же,
// что в doDeploy, но порядок свой — по важности для игрока: сначала то, что
// деньгами не исправить, потом очки действия и валюта, которую можно
// накопить.
func (s *State) buyBlock(side Side, t UnitType, have int) string {
	spec := Spec(t)
	switch {
	case spec.MaxAlive > 0 && have >= spec.MaxAlive:
		return WhyMax
	case s.ArmySize(side) >= ArmyCap(side):
		return WhyArmy
	case s.HiresLeft(side) <= 0:
		return WhyHires
	case s.AP < DeployAPCost:
		return WhyAP
	case s.silver[side] < spec.Silver:
		return WhySilver
	case s.supplies[side] < s.unitCost(side, t):
		return WhySupplies
	}
	return ""
}

// courtBlock — то же для стройки во дворе.
func (s *State) courtBlock(side Side, cb courtBuild, have, spots int) string {
	switch {
	case have >= cb.Max:
		return WhyMax
	case spots == 0:
		return WhyCourt
	case s.AP < ConstructAPCost:
		return WhyAP
	case s.supplies[side] < cb.Cost:
		return WhySupplies
	}
	return ""
}

// CargoView — отряд внутри осадной башни. Приезжает только её хозяину:
// сколько людей в башне и кто именно — его дело, а не противника.
type CargoView struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
	HP    int    `json:"hp"`
	MaxHP int    `json:"maxHp"`
	Spent bool   `json:"spent"` // уже ходил в этом ходу
}

// BuildingView — постройка на карте. В снимок попадает не всякая: чужую
// прячет туман ровно так же, как чужой отряд, см. visibleBuilding.
type BuildingView struct {
	ID    int    `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Side  string `json:"side"`
	Pos   Point  `json:"pos"`
	HP    int    `json:"hp"`
	MaxHP int    `json:"maxHp"`
	Mine  bool   `json:"mine"`
	Held  bool   `json:"held"`           // наблюдательный пункт держит мой отряд
	Hold  string `json:"hold,omitempty"` // чья она сейчас: для цвета на карте
	Life  int    `json:"life,omitempty"` // дней до сворачивания (аванпост)
}

// MineView — клетка рудника и кто её занял. Рудник на виду у всех.
type MineView struct {
	Pos  Point  `json:"pos"`
	Side string `json:"side"`
}

type UnitView struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
	Side  string `json:"side"`
	HP    int    `json:"hp"`
	MaxHP int    `json:"maxHp"`
	Pos   Point  `json:"pos"`
	Mine  bool   `json:"mine"`

	// Stars — звёзды ветерана. Видны всем: кого растили, видно по тому, как
	// он бьёт, и прятать это от противника нечестно.
	Stars int `json:"stars,omitempty"`

	// Moved и Acted — свой отряд в этом ходу уже ходил или уже действовал
	// (бил, строил, лечил). Приезжают только хозяину и только в его ход:
	// без них панель не отличит «некуда идти» от «уже отходил».
	Moved bool `json:"moved,omitempty"`
	Acted bool `json:"acted,omitempty"`

	Engine   bool    `json:"engine"`
	Reach    int     `json:"reach"`             // дальность с учётом башен: клиент рисует зону
	Area     int     `json:"area,omitempty"`    // радиус накрытия: бьёт по клетке, а не по цели
	Lurking  bool    `json:"lurking,omitempty"` // свой отряд затаился в кустах
	Moves    []Point `json:"moves,omitempty"`
	Targets  []Point `json:"targets,omitempty"`
	CanClimb bool    `json:"canClimb,omitempty"`

	// Подрывник: сколько зарядов осталось и под какие сегменты их можно
	// заложить прямо сейчас — выбирает игрок, а не геометрия.
	Charges  int     `json:"charges,omitempty"`
	ChargeAt []Point `json:"chargeAt,omitempty"`

	// Royal — король. Клиент рисует его отдельно: его нельзя терять и нельзя
	// достать издали.
	Royal     bool    `json:"royal,omitempty"`
	MeleeOnly bool    `json:"meleeOnly,omitempty"`
	Builder   bool    `json:"builder,omitempty"`
	BuildAt   []Point `json:"buildAt,omitempty"` // куда инженеры могут ставить
	FixAt     []Point `json:"fixAt,omitempty"`   // что инженеры могут чинить

	// FenceAt — готовые прясла частокола инженеров замка: по линии на
	// каждую годную клетку из BuildAt, первой точкой — сама клетка.
	// Считает их сервер, потому что крайние колья линии стоят в двух
	// клетках от инженера, и годность этих клеток клиенту знать не
	// откуда. По ним он и рисует призрак линии под курсором.
	FenceAt [][]Point `json:"fenceAt,omitempty"`

	// Лекарь: кого он может перевязать прямо сейчас.
	Healer bool    `json:"healer,omitempty"`
	HealAt []Point `json:"healAt,omitempty"`

	// Аванпост осаждающих: умеет ли этот отряд его разбить и куда.
	CanOutpost bool    `json:"canOutpost,omitempty"`
	OutpostAt  []Point `json:"outpostAt,omitempty"`

	// Наблюдательный пункт: его ставят инженеры замка и пехота осаждающих.
	CanWatch bool    `json:"canWatch,omitempty"`
	WatchAt  []Point `json:"watchAt,omitempty"`

	// Фашины: какие клетки рва этот отряд может завалить прямо сейчас и во
	// сколько очков действия ему это встанет — пехоте даром.
	CanFascine bool    `json:"canFascine,omitempty"`
	FascineAt  []Point `json:"fascineAt,omitempty"`
	FascineAP  int     `json:"fascineAp,omitempty"`

	// Склад в лагере: его ставит пехота осаждающих.
	CanStore bool    `json:"canStore,omitempty"`
	StoreAt  []Point `json:"storeAt,omitempty"`

	// Деревянные ворота: инженеры замка заколачивают ими пролом.
	GateAt []Point `json:"gateAt,omitempty"`

	// Мантелет: куда полевой инженер осады может выкатить собранный щит.
	MantletAt []Point `json:"mantletAt,omitempty"`

	// Осадная башня. Capacity и Ramp видят все — раскрытый трап не спрячешь;
	// Cargo приезжает только хозяину.
	Capacity int         `json:"capacity,omitempty"`
	Ramp     bool        `json:"ramp,omitempty"`
	CanRamp  bool        `json:"canRamp,omitempty"`
	Cargo    []CargoView `json:"cargo,omitempty"`
	UnloadAt []Point     `json:"unloadAt,omitempty"`
	ExitAt   []Point     `json:"exitAt,omitempty"`  // куда экипаж может вылезти без трапа
	BoardAt  []Point     `json:"boardAt,omitempty"` // куда этот отряд может сесть
}

type BuyView struct {
	Unit       string `json:"unit"`
	Label      string `json:"label"`
	Cost       int    `json:"cost"`
	Silver     int    `json:"silver"`
	AP         int    `json:"ap"` // цена в очках действия
	Elite      bool   `json:"elite"`
	Affordable bool   `json:"affordable"`
	Why        string `json:"why,omitempty"` // почему не нанять, см. Why*

	// Предел на число таких отрядов: клиент показывает «2/2» и гасит кнопку.
	Have int `json:"have,omitempty"`
	Max  int `json:"max,omitempty"`
}

// ViewFor собирает снимок для места seat (-1 — наблюдатель).
func (s *State) ViewFor(seat int) View {
	side := SideNeutral
	if seat >= 0 && seat < 3 {
		side = s.SideOf(seat)
	}
	myTurn := side == s.Turn && s.Phase == PhasePlay

	v := View{
		// Пустые срезы инициализируем явно: nil-срез уезжает в JSON как null,
		// и клиент спотыкается на нём ровно тогда, когда туман спрятал всё.
		Seats:     []SeatView{},
		Units:     []UnitView{},
		Buildings: []BuildingView{},
		Traps:     []Point{},
		Mine:      []MineView{},
		Farm:      []MineView{},
		Buyable:   []BuyView{},

		Moat: []Point{},

		Phase:    s.Phase.String(),
		Day:      s.Day,
		Days:     SiegeDays,
		Turn:     s.Turn.String(),
		TurnName: s.Seats[s.SeatOf(s.Turn)].Name,
		AP:       s.AP,
		APMax:    APPerTurn,
		Wall:     append([]WallSegment{}, s.Wall...),
		Result:   s.Result,
	}

	for i := 0; i < 3; i++ {
		sd := s.SideOf(i)
		v.Seats = append(v.Seats, SeatView{
			Seat:  i,
			Name:  s.Seats[i].Name,
			Score: s.Seats[i].Score,
			Side:  sd.String(),
			Turn:  sd == s.Turn,
		})
	}

	v.You = YouView{
		Zone:     []Point{},
		Seat:     seat,
		Side:     side.String(),
		Supplies: s.supplies[side],
		Silver:   s.silver[side],
		Mine:     s.MineHeldBy(side),
		Farms:    s.FarmsHeldBy(side),
		MyTurn:   myTurn,

		SupplyCap:   s.SupplyCap(side),
		IncomeParts: []IncomePart{},
	}
	// Наблюдателю доход не положен: он ни за кого не играет.
	if side != SideNeutral {
		parts, silver := s.Income(side)
		v.You.Income, v.You.IncomeParts, v.You.SilverIncome = IncomeTotal(parts), parts, silver
	}
	vis := s.VisibleFor(side)
	v.Fog = fogRows(vis)
	if open := s.moatOpenFor(side, vis); len(open) > 0 {
		v.Moat = open
	}

	// Постройки прячет туман: чужой частокол, вышка или аванпост попадают в
	// снимок, только пока на их клетку кто-то смотрит.
	for _, b := range s.Buildings {
		if !s.visibleBuilding(b, side, vis) {
			continue
		}
		bv := BuildingView{
			ID: b.ID, Kind: b.Kind.String(), Label: b.Kind.Label(),
			Side: b.Owner.String(), Pos: b.Pos, HP: b.HP, MaxHP: b.MaxHP,
			Mine: b.Owner == side,
			Held: s.towerHeldBy(b, side),
		}
		if b.Expires > 0 {
			bv.Life = b.Expires - s.Day + 1
		}
		if b.Kind == BuildTower {
			bv.Hold = s.towerOwner(b).String()
		}
		v.Buildings = append(v.Buildings, bv)
	}
	// Ловушки видит только тот, кто их ставил.
	for _, t := range s.Traps {
		if t.Owner == side {
			v.Traps = append(v.Traps, t.Pos)
		}
	}
	for _, p := range MineTiles() {
		mv := MineView{Pos: p, Side: SideNeutral.String()}
		// Кто держит рудник, видно только если клетка просматривается.
		if u := s.unitAt(p); u != nil && (u.Owner == side || vis[p]) {
			mv.Side = u.Owner.String()
		}
		v.Mine = append(v.Mine, mv)
	}
	// Пашни — та же логика, что у рудника: клетка на виду, но кто на ней
	// стоит, видно только через свой обзор.
	for _, p := range FarmTiles() {
		fv := MineView{Pos: p, Side: SideNeutral.String()}
		if u := s.unitAt(p); u != nil && (u.Owner == side || vis[p]) {
			fv.Side = u.Owner.String()
		}
		v.Farm = append(v.Farm, fv)
	}
	v.You.Towers = s.towersHeld(side)
	v.You.Army, v.You.ArmyMax = s.ArmySize(side), ArmyCap(side)
	v.You.Rally = s.Rallied(side)
	v.You.RallyOK = myTurn && !v.You.Rally && s.supplies[side] >= RallyCost && s.AP >= RallyAP
	v.You.MilitiaOK = myTurn && side == SideDefender && s.MilitiaReady()
	if myTurn && side == SideDefender {
		v.You.MilitiaWhy = s.militiaBlock()
	}
	v.You.Rebuild = s.rebuildCost(side)
	v.You.Watch, v.You.WatchMax = WatchPostCost(side), WatchPostMax
	v.You.WatchHad = s.countBuildings(side, BuildTower)
	v.You.Fixes, v.You.FixesMax = s.RepairsLeft(side), MaxWallRepairs
	v.You.Hires, v.You.HiresMax = s.HiresLeft(side), MaxDeploysPerTurn
	v.You.Pitches, v.You.PitchesMax = s.PitchesLeft(side), MaxPitchPerTurn
	if k := s.King(side); k != nil {
		v.You.King = &KingView{ID: k.ID, HP: k.HP, MaxHP: k.Spec().MaxHP, Pos: k.Pos}
	}
	if myTurn {
		v.You.Zone = s.deployTiles(side)
		if side == SideDefender {
			v.You.Court = s.courtSpots()
			for _, key := range CourtBuildOrder {
				cb := courtBuilds[key]
				have := s.countBuildings(side, cb.Kind)
				why := s.courtBlock(side, cb, have, len(v.You.Court))
				v.You.Builds = append(v.You.Builds, CourtOption{
					Key: key, Label: cb.Kind.Label(), Cost: cb.Cost, AP: ConstructAPCost, Hint: cb.Hint,
					Have: have, Max: cb.Max,
					Affordable: why == "",
					Why:        why,
				})
			}
		}
		for _, t := range buyListFor(side) {
			spec := Spec(t)
			have := s.countUnits(side, t)
			why := s.buyBlock(side, t, have)
			v.Buyable = append(v.Buyable, BuyView{
				Unit:       spec.Name,
				Label:      spec.Label,
				Cost:       s.unitCost(side, t),
				Silver:     spec.Silver,
				AP:         DeployAPCost,
				Elite:      spec.Elite,
				Affordable: why == "",
				Why:        why,
				Have:       have,
				Max:        spec.MaxAlive,
			})
		}
	}

	for _, u := range s.Units {
		// Пассажиры осадной башни на карте не стоят: их показывает сама
		// башня, списком, и только своему хозяину.
		if u.Carrier != 0 {
			continue
		}
		// Туман войны и засада: чужие отряды попадают в снимок, только пока
		// их кто-то из наших видит — и только если они не затаились в кустах.
		if u.Owner != side && !s.VisibleUnit(u, side) {
			continue
		}
		uv := UnitView{
			ID:        u.ID,
			Type:      u.Type.String(),
			Label:     u.Spec().Label,
			Side:      u.Owner.String(),
			HP:        u.HP,
			MaxHP:     u.MaxHP(),
			Stars:     u.Stars(),
			Pos:       u.Pos,
			Mine:      u.Owner == side,
			Engine:    u.Spec().Engine,
			Royal:     u.Spec().Royal,
			MeleeOnly: u.Spec().MeleeOnly,
			Capacity:  u.Spec().Capacity,
			Ramp:      u.Ramp,
		}
		// Своему хозяину показываем, что отряд ещё не раскрыт: по этому
		// признаку и решают, бить из засады или подождать.
		if u.Owner == side {
			uv.Lurking = s.Lurking(u)
			// Кто едет внутри — дело хозяина башни, и видеть это он должен
			// всегда, а не только в свой ход.
			for _, c := range s.cargoOf(u) {
				uv.Cargo = append(uv.Cargo, CargoView{
					ID: c.ID, Type: c.Type.String(), Label: c.Spec().Label,
					HP: c.HP, MaxHP: c.MaxHP(),
					Spent: c.movedTurn == s.turnCounter,
				})
			}
		}
		// Дальность отдаём по всем юнитам: зоны обстрела — открытая
		// информация, по ней игрок решает, куда безопасно шагнуть.
		if u.Spec().Damage > 0 {
			uv.Reach = s.unitRange(u)
			uv.Area = u.Spec().Splash
		}
		if u.Owner == side {
			uv.Charges = u.Charges
			if myTurn {
				uv.Moved = u.movedTurn == s.turnCounter
				uv.Acted = u.actedTurn == s.turnCounter
				uv.Moves = s.Reachable(u)
				uv.Targets = s.AttackTargets(u)
				uv.CanClimb = s.canClimb(u)
				uv.ChargeAt = s.chargeTargets(u)
				// Строят обе стороны, но разное: замок чинит и заколачивает,
				// осада ставит частокол и катит щиты.
				if u.Spec().Builder {
					uv.Builder = true
					uv.BuildAt, uv.FixAt = s.builderSpots(u)
					if side == SideDefender {
						uv.GateAt = s.woodGateSpots(u)
						uv.FenceAt = s.fenceLines(u)
					} else {
						uv.FixAt = nil // чинить осаде нечем: это умение замка
						uv.MantletAt = s.mantletSpots(u)
					}
				}
				if u.Spec().Healer {
					uv.Healer = true
					uv.HealAt = s.healTargets(u)
				}
				// Кнопку аванпоста показываем всем подходящим отрядам, даже
				// если рядом нет годной клетки: иначе о ней не догадаться.
				uv.CanOutpost = u.Owner.IsAttacker() && !u.Spec().Mechanism()
				uv.OutpostAt = s.outpostSpots(u)
				// Вышку — тем же способом: кнопка видна тому, кто в принципе
				// умеет её ставить, даже если прямо сейчас ставить некуда.
				uv.CanWatch = (side == SideDefender && u.Spec().Builder) ||
					(side.IsAttacker() && (u.Type == UnitInfantry || u.Type == UnitSapper))
				uv.WatchAt = s.watchPostSpots(u)
				// Фашины и склад — работа осаждающих: кнопки показываем
				// тем, кто в принципе на это годен, даже если прямо сейчас
				// рядом нет ни рва, ни места под обоз.
				uv.CanFascine = side.IsAttacker() && !u.Spec().Mechanism()
				uv.FascineAt = s.fascineSpots(u)
				if uv.CanFascine {
					uv.FascineAP = fascineAP(u)
				}
				uv.CanStore = side.IsAttacker() && u.Type == UnitInfantry
				uv.StoreAt = s.campWarehouseSpots(u)
				uv.BoardAt = s.boardTargets(u)
				if u.Spec().Capacity > 0 {
					uv.CanRamp = s.canRamp(u)
					uv.UnloadAt = s.unloadSpots(u)
					uv.ExitAt = s.exitSpots(u)
				}
			}
		}
		v.Units = append(v.Units, uv)
	}
	return v
}

func (s *State) deployTiles(side Side) []Point {
	var out []Point
	for y := 0; y < Rows; y++ {
		for x := 0; x < Cols; x++ {
			p := Point{x, y}
			if s.deployZone(side, p) && s.unitAt(p) == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// AttackTargets — все клетки, по которым юнит может ударить прямо сейчас.
func (s *State) AttackTargets(u *Unit) []Point {
	spec := u.Spec()
	if spec.Damage <= 0 || u.actedTurn == s.turnCounter {
		return nil
	}
	reach := s.unitRange(u)
	// Навесное оружие бьёт по клетке: цель выбирается из всей зоны обстрела,
	// её рисует клиент по полю reach. Отдельный список здесь не нужен.
	if spec.Splash > 0 {
		return nil
	}
	var out []Point
	for y := 0; y < Rows; y++ {
		for x := 0; x < Cols; x++ {
			p := Point{x, y}
			d := Chebyshev(u.Pos, p)
			if d < 1 || d > reach {
				continue
			}
			t := s.unitAt(p)
			// Пустая клетка стены — кладка, занятая — цель. Отряд в проломе
			// или в воротах бьют как любого другого: именно из-за обратного
			// порядка он раньше и оказывался неуязвимым.
			if t == nil && TileAt(p) == TileWall {
				seg := s.segment(p.X)
				if seg == nil || seg.Breached() || !u.Owner.IsAttacker() || spec.VsWall <= 0 {
					continue
				}
				if spec.GateOnly && seg.Kind != SegGate {
					continue
				}
				out = append(out, p)
				continue
			}
			// Постройку бьём, только если в ней никто не засел: отряд в
			// вышке принимает удар на себя.
			if b := s.buildingAt(p); b != nil && t == nil {
				if areEnemies(u.Owner, b.Owner) && !spec.GateOnly {
					out = append(out, p)
				}
				continue
			}
			if t == nil || !areEnemies(u.Owner, t.Owner) || spec.NoUnits {
				continue
			}
			if !s.VisibleUnit(t, u.Owner) {
				continue // в тумане и в засаде цель не выбрать
			}
			if !meleeReach(spec, t, d) {
				continue // короля издали не достать
			}
			out = append(out, p)
		}
	}
	return out
}

// builderSpots — куда инженеры могут ставить и что чинить прямо сейчас.
//
// В ремонт попадает всё, до чего инженеры дотягиваются: стоящая повреждённая
// кладка и ворота, свои постройки и свои осадные машины. Лежащего пролома в
// списке нет — его не латают, а отстраивают или заколачивают (GateAt).
func (s *State) builderSpots(u *Unit) (build []Point, fix []Point) {
	if u.actedTurn == s.turnCounter {
		return nil, nil
	}
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		if !p.InBounds() {
			continue
		}
		if s.outdoorBuildable(p) {
			build = append(build, p)
			continue
		}
		if TileAt(p) == TileWall {
			if seg := s.segment(p.X); seg != nil && !seg.Breached() && seg.HP < seg.MaxHP {
				fix = append(fix, p)
			}
			continue
		}
		// Своя осадная машина рядом: баллисту и требушет чинят инженеры —
		// лекарь к ним не подходит.
		if t := s.unitAt(p); t != nil {
			if t.Owner == u.Owner && t.Spec().Mechanism() && t.HP < t.MaxHP() {
				fix = append(fix, p)
			}
			continue
		}
		if b := s.buildingAt(p); b != nil && b.Owner == u.Owner && b.HP < b.MaxHP {
			fix = append(fix, p)
		}
	}
	sortPoints(build)
	sortPoints(fix)
	return build, fix
}

// visibleBuilding — попадает ли постройка в снимок этой стороны. Правило то
// же, что у отрядов: своё и союзное по осаде видно всегда, чужое — только
// пока его клетку кто-то просматривает. Раньше постройки шли в снимок
// скопом, и чужой частокол в тумане читался с карты, как свой: туман
// гасил цвет, но не прятал саму постройку.
//
// Ничейный мост у ворот — исключение: он стоит на карте с первого дня и
// постройкой стороны не является. Прятать его значило бы показывать на его
// месте открытую воду, то есть врать, а не умалчивать.
func (s *State) visibleBuilding(b *Building, side Side, vis map[Point]bool) bool {
	if b.Owner == SideNeutral || !areEnemies(b.Owner, side) {
		return true
	}
	return vis[b.Pos]
}

// moatOpenFor — какие клетки рва выглядят открытой водой **для этой
// стороны**. Настил, которого не видно, воду не закрывает: иначе чужие
// фашины читались бы по цвету клетки сквозь туман, и прятать саму вязанку
// не имело бы смысла.
func (s *State) moatOpenFor(side Side, vis map[Point]bool) []Point {
	var out []Point
	for _, p := range MoatTiles() {
		b := s.buildingAt(p)
		if b != nil && b.Kind.Deck() && s.visibleBuilding(b, side, vis) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// fenceLines — прясла частокола на каждую клетку, куда инженер замка может
// его поставить. Клиент подсвечивает линию под курсором этим же списком, а
// не считает её заново: иначе о том, куда встанет частокол, было бы две
// правды — и расходились бы они как раз на занятых клетках.
func (s *State) fenceLines(u *Unit) [][]Point {
	if u.Owner != SideDefender || !u.Spec().Builder || u.actedTurn == s.turnCounter {
		return nil
	}
	var out [][]Point
	for _, off := range neighborOffsets {
		p := u.Pos.Add(off[0], off[1])
		if p.InBounds() && s.outdoorBuildable(p) {
			out = append(out, s.fenceLine(u.Pos, p))
		}
	}
	return out
}

// canClimb — может ли отряд подняться по соседней лестнице прямо сейчас.
// Лезут все пешие отряды осаждающих, машины остаются внизу; подъём считается
// действием, поэтому подойти и влезть можно в один день.
func (s *State) canClimb(u *Unit) bool {
	if !u.Owner.IsAttacker() || u.Spec().Mechanism() || u.actedTurn == s.turnCounter {
		return false
	}
	ladder := s.adjacentLadder(u.Pos)
	if ladder == nil {
		return false
	}
	return s.unitAt(Point{ladder.Pos.X, WalkRow}) == nil
}
