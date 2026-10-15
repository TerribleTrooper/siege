package game

// Постройки и инженерные сооружения. Стена — отдельная сущность (сегменты),
// а всё остальное, что стоит на карте и имеет прочность, живёт здесь.

type BuildingKind uint8

const (
	BuildBarracks   BuildingKind = iota // казармы: доход замка
	BuildTower                          // наблюдательный пункт: обзор и +дальность
	BuildWorkshop                       // осадная мастерская: скидка на машины
	BuildBarricade                      // частокол инженеров замка: перекрывает проход
	BuildOutpost                        // временный аванпост осаждающих: вторая точка найма
	BuildArrowTower                     // стрелковая башня замка: +дальность своим стрелкам
	BuildForge                          // кузница: ремонт стены сильнее и дешевле
	BuildWarehouse                      // склад: поднимает потолок обоза
	BuildFascine                        // настил из вязанок: переправа через ров
	BuildPalisade                       // частокол осаждающих: тот же, только их
	BuildBridge                         // мост напротив ворот: ничей, стоит с начала осады
)

// Внутренние имена — ключи протокола, и у замкового частокола он остался
// barricade: вид постройки отдельный (свой хозяин, свой обзор, своя цена за
// линию), и переименовать ключ значило бы столкнуть его с частоколом осады.
// Игроку оба показываются одинаково — «Частокол», см. buildingLabels.
var buildingNames = map[BuildingKind]string{
	BuildBarracks:   "barracks",
	BuildTower:      "tower",
	BuildWorkshop:   "workshop",
	BuildBarricade:  "barricade",
	BuildOutpost:    "outpost",
	BuildArrowTower: "arrowTower",
	BuildForge:      "forge",
	BuildWarehouse:  "warehouse",
	BuildFascine:    "fascine",
	BuildPalisade:   "palisade",
	BuildBridge:     "bridge",
}

var buildingLabels = map[BuildingKind]string{
	BuildBarracks:   "Казармы",
	BuildTower:      "Наблюдательный пункт",
	BuildWorkshop:   "Осадная мастерская",
	BuildBarricade:  "Частокол",
	BuildOutpost:    "Аванпост",
	BuildArrowTower: "Стрелковая башня",
	BuildForge:      "Кузница",
	BuildWarehouse:  "Склад",
	BuildFascine:    "Фашины",
	BuildPalisade:   "Частокол",
	BuildBridge:     "Мост",
}

func (k BuildingKind) String() string { return buildingNames[k] }
func (k BuildingKind) Label() string  { return buildingLabels[k] }

func buildingHP(k BuildingKind) int {
	switch k {
	case BuildBarracks:
		return BarracksHP
	case BuildTower:
		return TowerBuildHP
	case BuildWorkshop:
		return WorkshopHP
	case BuildBarricade:
		return BarricadeHP
	case BuildOutpost:
		return OutpostHP
	case BuildArrowTower:
		return ArrowTowerHP
	case BuildForge:
		return ForgeHP
	case BuildWarehouse:
		return WarehouseHP
	case BuildFascine:
		return FascineHP
	case BuildPalisade:
		return PalisadeHP
	case BuildBridge:
		return BridgeHP
	}
	return 10
}

// Enterable — пускает ли постройка отряд внутрь. Такая одна: наблюдательный
// пункт. В него садятся, из него стреляют дальше и сильнее и в нём
// отсиживаются от стрел — потому за вышки и идёт драка.
func (k BuildingKind) Enterable() bool { return k == BuildTower }

// Deck — настил, а не препятствие: по нему ходят все подряд, включая
// машины, которым внутрь вышки хода нет. Настилов два: брошенные в ров
// вязанки и мост у ворот. Через ров проходят только по ним — вода
// непроходима, см. tilePassable.
func (k BuildingKind) Deck() bool { return k == BuildFascine || k == BuildBridge }

// Building — постройка на карте. Занимает клетку и не пускает через себя
// (кроме наблюдательного пункта, см. Enterable).
type Building struct {
	ID    int          `json:"id"`
	Kind  BuildingKind `json:"-"`
	Owner Side         `json:"-"`
	Pos   Point        `json:"pos"`
	HP    int          `json:"hp"`
	MaxHP int          `json:"maxHp"`

	// Expires — день, после которого постройка сворачивается сама.
	// 0 — стоит до конца осады.
	Expires int `json:"-"`
}

// Trap — ловушка инженеров. Осаждающие её не видят, пока не наступят.
type Trap struct {
	Pos   Point `json:"pos"`
	Owner Side  `json:"-"`
}

// startingBuildings — что стоит на карте в начале осады. Расстановка
// зеркальна: преимущество одного из осаждающих ломало бы всю игру.
func startingBuildings(nextID func() int) []*Building {
	add := func(out []*Building, kind BuildingKind, owner Side, p Point) []*Building {
		return append(out, &Building{
			ID: nextID(), Kind: kind, Owner: owner, Pos: p,
			HP: buildingHP(kind), MaxHP: buildingHP(kind),
		})
	}
	var out []*Building
	// Замок: двое казарм по флангам двора.
	out = add(out, BuildBarracks, SideDefender, Point{WallFirstCol + 2, CourtRowFirst + 1})
	out = add(out, BuildBarracks, SideDefender, Point{WallLastCol - 2, CourtRowFirst + 1})
	// Осаждающим — по мастерской в глубине лагеря.
	out = add(out, BuildWorkshop, SideAttackerA, Point{3, CampRowLast})
	out = add(out, BuildWorkshop, SideAttackerB, Point{Cols - 4, CampRowLast})
	// Мост напротив ворот: две клетки рва, ничьи. Единственная переправа,
	// которая есть у осады с первого дня, — и первое, что защитник захочет
	// сломать, чтобы вернуть воду под стену.
	out = add(out, BuildBridge, SideNeutral, Point{GateColLeft, MoatRow})
	out = add(out, BuildBridge, SideNeutral, Point{GateColRight, MoatRow})
	// Готовых вышек на карте нет: наблюдательные пункты стороны ставят сами.
	return out
}

func (s *State) buildingAt(p Point) *Building {
	for _, b := range s.Buildings {
		if b.Pos == p {
			return b
		}
	}
	return nil
}

func (s *State) trapAt(p Point) *Trap {
	for i := range s.Traps {
		if s.Traps[i].Pos == p {
			return &s.Traps[i]
		}
	}
	return nil
}

func (s *State) removeBuilding(b *Building) {
	for i, x := range s.Buildings {
		if x == b {
			s.Buildings = append(s.Buildings[:i], s.Buildings[i+1:]...)
			return
		}
	}
}

func (s *State) removeTrap(p Point) {
	for i := range s.Traps {
		if s.Traps[i].Pos == p {
			s.Traps = append(s.Traps[:i], s.Traps[i+1:]...)
			return
		}
	}
}

// countBuildings — сколько целых построек данного вида у стороны.
func (s *State) countBuildings(side Side, kind BuildingKind) int {
	n := 0
	for _, b := range s.Buildings {
		if b.Owner == side && b.Kind == kind {
			n++
		}
	}
	return n
}

// WatchPostCost — во что обходится наблюдательный пункт этой стороне.
// Осаждающим дороже: материал они тащат от края карты, замок берёт со двора.
func WatchPostCost(side Side) int {
	if side == SideDefender {
		return WatchPostCostDef
	}
	return WatchPostCostAtk
}

// outpostNear — стоит ли рядом целый аванпост этой стороны. Пока стоит,
// отряды можно нанимать прямо у него, а не только в лагере у края карты.
func (s *State) outpostNear(side Side, p Point) bool {
	for _, b := range s.Buildings {
		if b.Kind == BuildOutpost && b.Owner == side && Chebyshev(b.Pos, p) <= OutpostDeployAt {
			return true
		}
	}
	return false
}

// expireBuildings сворачивает постройки, у которых вышел срок. Зовётся при
// смене дня: аванпост временный, он не должен стоять всю осаду.
func (s *State) expireBuildings() {
	for _, b := range append([]*Building{}, s.Buildings...) {
		if b.Expires > 0 && s.Day > b.Expires {
			s.removeBuilding(b)
		}
	}
}

// towerOwner — кто **контролирует** наблюдательный пункт. Сидящий внутри
// отряд забирает его сразу; снаружи вышка достаётся той стороне, чьи отряды
// стоят рядом в одиночку. Если у вышки топчутся двое — она ничья: спорную
// надо сначала расчистить.
//
// Пустая и никем не обступленная вышка остаётся за тем, кто её построил:
// иначе тридцать припасов приходилось бы ещё и караулить отрядом, и строить
// их не стал бы никто.
func (s *State) towerOwner(b *Building) Side {
	if b.Kind != BuildTower {
		return b.Owner
	}
	if u := s.unitAt(b.Pos); u != nil {
		return u.Owner
	}
	holder := SideNeutral
	for _, u := range s.Units {
		if Chebyshev(u.Pos, b.Pos) > TowerHoldRange {
			continue
		}
		switch {
		case holder == SideNeutral:
			holder = u.Owner
		case holder != u.Owner:
			return SideNeutral // вышка оспорена
		}
	}
	if holder == SideNeutral {
		return b.Owner
	}
	return holder
}

// towerHeldBy — держит ли сторона эту постройку.
func (s *State) towerHeldBy(b *Building, side Side) bool {
	return s.towerOwner(b) == side
}

// towersHeld — сколько наблюдательных пунктов за стороной. По ним идёт
// прибавка к припасам: вышка — не только обзор, но и перевалочный пункт обоза.
func (s *State) towersHeld(side Side) int {
	n := 0
	for _, b := range s.Buildings {
		if b.Kind == BuildTower && s.towerOwner(b) == side {
			n++
		}
	}
	return n
}

// towerBonus — прибавка к дальности от башни. Сидящему внутри достаётся
// больше, чем стоящему рядом, а с оспоренной башни не капает никому.
func (s *State) towerBonus(u *Unit) int {
	if u.Spec().Range <= 1 {
		return 0
	}
	best := 0
	for _, b := range s.Buildings {
		switch b.Kind {
		case BuildTower:
			if s.towerOwner(b) != u.Owner {
				continue
			}
			bonus := TowerRangeBonus
			if b.Pos == u.Pos {
				bonus = TowerGarrisonBonus
			}
			if Chebyshev(b.Pos, u.Pos) <= TowerHoldRange && bonus > best {
				best = bonus
			}
		case BuildArrowTower:
			// Стрелковая башня замка работает только на своих и дальше.
			if b.Owner == u.Owner && Chebyshev(b.Pos, u.Pos) <= ArrowTowerRange && TowerRangeBonus > best {
				best = TowerRangeBonus
			}
		}
	}
	return best
}

// inTower — сидит ли отряд внутри наблюдательного пункта: от этого зависит
// укрытие от стрел.
func (s *State) inTower(p Point) bool {
	b := s.buildingAt(p)
	return b != nil && b.Kind == BuildTower
}

// forgeBonus — работает ли у замка кузница: ремонт стены сильнее и дешевле.
func (s *State) forgeBonus(side Side) bool {
	return side == SideDefender && s.countBuildings(side, BuildForge) > 0
}

// buildingVision — насколько далеко постройка просматривает окрестности.
func buildingVision(k BuildingKind) int {
	switch k {
	case BuildBarracks:
		return VisionBarracks
	case BuildWorkshop:
		return VisionWorkshop
	case BuildTower:
		return VisionTower
	case BuildBarricade:
		return VisionBarricade
	case BuildOutpost:
		return VisionOutpost
	case BuildArrowTower:
		return VisionTower
	case BuildForge:
		return VisionWorkshop
	case BuildWarehouse:
		return VisionBarracks
	}
	return 0
}

// unitRange — дальность юнита с учётом башен.
func (s *State) unitRange(u *Unit) int {
	r := u.Spec().Range
	if r <= 1 {
		return r
	}
	return r + s.towerBonus(u)
}

// engineDiscount — скидка на осадные машины от уцелевшей мастерской.
func (s *State) engineDiscount(side Side, t UnitType) int {
	if !Spec(t).Engine || s.countBuildings(side, BuildWorkshop) == 0 {
		return 0
	}
	return WorkshopCut
}

// unitCost — цена найма с учётом построек.
func (s *State) unitCost(side Side, t UnitType) int {
	cost := Spec(t).Cost - s.engineDiscount(side, t)
	if cost < 0 {
		return 0
	}
	return cost
}
