package game

import "fmt"

// Геометрия карты и всё, что связано с проходимостью.
//
//	y=0..2  двор: король, казармы, стрелковые башни, рудник в углу
//	y=3     боевой ход: здесь стоят защитники
//	y=4     линия стены: сегменты, башни, ворота
//	y=5     ров с водой: не переходит никто, только по настилу
//	y=6..19 поле: кусты, обрывы, рудник
//	y=20,21 лагеря осаждающих
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func (p Point) Add(dx, dy int) Point { return Point{p.X + dx, p.Y + dy} }

func (p Point) InBounds() bool { return p.X >= 0 && p.X < Cols && p.Y >= 0 && p.Y < Rows }

// Chebyshev — расстояние на квадратной сетке: шаг по диагонали стоит столько
// же, сколько прямой. По ней считаются и перемещение, и дальность стрельбы,
// поэтому зона досягаемости выглядит квадратом, а не ромбом.
func Chebyshev(a, b Point) int {
	dx, dy := abs(a.X-b.X), abs(a.Y-b.Y)
	if dx > dy {
		return dx
	}
	return dy
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

type TileKind uint8

const (
	TileField TileKind = iota
	TileCamp
	TileWall
	TileWalk
	TileCourtyard
	// TileCliff — скалы по флангам замка. Без них стену можно было бы
	// просто обойти по краю карты, и вся осада теряла бы смысл.
	TileCliff
	// TileRavine — обрывы на поле: непроходимы, но простреливаются.
	TileRavine
	// TileBush — кусты: укрытие. Входить дороже, и дальний урон по цели в
	// них срезается — но стрелять через них можно.
	TileBush
	// TileMine — клетка рудника: приносит серебро тому, кто на ней стоит.
	TileMine
	// TileFarm — пашня по флангу: обоз подвозит припасы тому, кто её занял.
	TileFarm
	// TileMoat — ров с водой вдоль стены. В воду не входит никто: ни пеший,
	// ни колёсный. Засыпанная фашинами клетка перестаёт быть препятствием —
	// но это состояние партии, а не рельефа, поэтому живёт оно в State, а не
	// здесь.
	TileMoat
)

var tileKindNames = map[TileKind]string{
	TileField:     "field",
	TileCamp:      "camp",
	TileWall:      "wall",
	TileWalk:      "walk",
	TileCourtyard: "courtyard",
	TileCliff:     "cliff",
	TileRavine:    "ravine",
	TileBush:      "bush",
	TileMine:      "mine",
	TileFarm:      "farm",
	TileMoat:      "moat",
}

func (k TileKind) String() string { return tileKindNames[k] }

// TileAt — статический тип клетки, без учёта проломов, построек и юнитов.
func TileAt(p Point) TileKind {
	switch {
	case !p.InBounds():
		return TileField
	case p.Y <= WallRow && (p.X < WallFirstCol || p.X > WallLastCol):
		return TileCliff // замок стоит в теснине: фланги непроходимы
	case p == CastleMine():
		return TileMine // рудник в углу двора: проверяем до самого двора
	case p.Y >= CourtRowFirst && p.Y <= CourtRowLast:
		return TileCourtyard
	case p.Y == WalkRow:
		return TileWalk
	case p.Y == WallRow:
		return TileWall
	case p.Y == MoatRow && p.X >= WallFirstCol && p.X <= WallLastCol:
		return TileMoat
	case p.Y >= CampRowFirst && isCampCol(p.X):
		return TileCamp
	}
	if k, ok := terrain[p]; ok {
		return k
	}
	return TileField
}

func isCampCol(x int) bool { return x < CampWidth || x >= Cols-CampWidth }

// CampSide — какому осаждающему принадлежит клетка лагеря.
func CampSide(p Point) Side {
	if p.Y < CampRowFirst || p.Y > CampRowLast {
		return SideNeutral
	}
	switch {
	case p.X < CampWidth:
		return SideAttackerA
	case p.X >= Cols-CampWidth:
		return SideAttackerB
	}
	return SideNeutral
}

// IsInsideCastle — клетки, куда осаждающий попадает только через пролом.
// Они же — весь мир короля: за эту черту он не выходит.
func IsInsideCastle(p Point) bool {
	switch TileAt(p) {
	case TileWalk, TileCourtyard:
		return true
	}
	return false
}

// ThroneTile — где король стоит в начале осады: середина двора, ровно за
// воротами. Дальше он ходит сам.
func ThroneTile() Point { return Point{GateColLeft, ThroneRow} }

// IsMine — клетка рудника.
func IsMine(p Point) bool { return TileAt(p) == TileMine }

// CastleMine — рудник в углу двора замка.
func CastleMine() Point { return Point{CastleMineCol, CastleMineRow} }

// MineTiles — все клетки рудника: две в поле и одна во дворе замка.
//
// Полевые разведены по вертикали: северная ближе к замку, южная — к лагерям.
// Зеркальной парой они не будут: на карте чётной ширины центральной колонки
// нет. Северная при этом равноудалена от обоих лагерей, южная на клетку
// ближе ко второму осаждающему — цена того, что рудников именно два, а не
// четыре. Главное здесь другое: между ними шесть рядов зарослей, и держать
// оба разом не выходит.
func MineTiles() []Point {
	return []Point{
		{MineColLeft, MineRow - MineGap},  // со стороны замка
		{MineColRight, MineRow + MineGap}, // со стороны лагерей
		CastleMine(),
	}
}

// FarmTiles — пашни по флангам: по одной у каждого края карты.
func FarmTiles() []Point {
	return []Point{{FarmColLeft, FarmRow}, {Cols - 1 - FarmColLeft, FarmRow}}
}

// IsFarm — клетка пашни.
func IsFarm(p Point) bool { return TileAt(p) == TileFarm }

// MoatTiles — вся канава вдоль стены, от края кладки до края.
func MoatTiles() []Point {
	out := make([]Point, 0, SegmentCount)
	for col := WallFirstCol; col <= WallLastCol; col++ {
		out = append(out, Point{col, MoatRow})
	}
	return out
}

// IsMoat — клетка рва. Засыпана она или нет, здесь не видно: это состояние
// партии, см. State.moatOpen.
func IsMoat(p Point) bool { return TileAt(p) == TileMoat }

// MoveCost — во сколько обходится вход в клетку по рельефу. Ров сюда не
// входит: засыпан он или нет, знает только State (см. State.moveCost).
func MoveCost(p Point) int {
	if TileAt(p) == TileBush {
		return BushMoveCost
	}
	return 1
}

// InBush — стоит ли точка в кустах: укрытие срезает дальний урон.
func InBush(p Point) bool { return TileAt(p) == TileBush }

// terrain — рельеф поля. Карта зеркальна относительно вертикальной оси
// (x ↔ Cols-1-x): у обоих осаждающих одинаковые подходы, иначе гонка за
// пролом была бы нечестной.
var terrain = buildTerrain()

func buildTerrain() map[Point]TileKind {
	m := map[Point]TileKind{}
	// put ставит клетку и её зеркальное отражение; перечисляем левую половину.
	put := func(y int, kind TileKind, xs ...int) {
		for _, x := range xs {
			m[Point{x, y}] = kind
			m[Point{Cols - 1 - x, y}] = kind
		}
	}

	// Кусты перед стеной: подойти можно скрытно, но медленно.
	put(6, TileBush, 6, 7, 11, 12)
	put(8, TileBush, 9, 10)
	// Обрывы режут фланги на коридоры — по краю карты уже не пробежать.
	put(7, TileRavine, 2, 3)
	put(8, TileRavine, 2, 3)
	put(16, TileRavine, 2, 3)
	put(17, TileRavine, 2, 3)
	put(10, TileRavine, 7, 8)
	put(14, TileRavine, 7, 8)
	// Заросли в центре поля: между двумя рудниками, и к обоим подбираются
	// через них — центр берут ближним боем.
	put(10, TileBush, 13)
	put(11, TileBush, 11, 12)
	put(12, TileBush, 10)
	put(13, TileBush, 11, 12)
	put(14, TileBush, 13)
	// Южная половина поля — зеркало северной.
	put(16, TileBush, 9, 10)
	put(18, TileBush, 6, 7, 11, 12)

	for _, p := range MineTiles() {
		if p.Y >= FieldRowFirst {
			m[p] = TileMine // двор к рельефу поля не относится: он в TileAt
		}
	}
	// Пашни по флангам: стоять на них невыгодно позиционно (край карты, до
	// центра далеко), зато обоз подвозит припасы каждый ход.
	for _, p := range FarmTiles() {
		m[p] = TileFarm
	}
	return m
}

// SegmentKind различает участки стены.
type SegmentKind uint8

const (
	SegWall SegmentKind = iota
	SegGate
	SegTower
	// SegWoodGate — деревянные ворота, которыми инженеры заколачивают пролом.
	// Такие же створки с решёткой, только прочности в них вчетверо меньше.
	SegWoodGate
)

var segmentKindNames = map[SegmentKind]string{
	SegWall: "wall", SegGate: "gate", SegTower: "tower", SegWoodGate: "woodgate",
}

func (k SegmentKind) String() string { return segmentKindNames[k] }

// MarshalJSON отдаёт вид сегмента именем, а не числом: клиенту надо
// отличать ворота от кладки, а делать это по индексу перечисления он не
// должен.
func (k SegmentKind) MarshalJSON() ([]byte, error) {
	return []byte(`"` + k.String() + `"`), nil
}

// UnmarshalJSON читает то же имя обратно: снимок должен разбираться в те же
// структуры, из которых он собран, иначе на нём спотыкаются и тесты, и
// любой разбор ответа сервера.
func (k *SegmentKind) UnmarshalJSON(b []byte) error {
	name := string(b)
	if len(name) >= 2 && name[0] == '"' {
		name = name[1 : len(name)-1]
	}
	for kind, n := range segmentKindNames {
		if n == name {
			*k = kind
			return nil
		}
	}
	return fmt.Errorf("неизвестный вид сегмента стены: %q", name)
}

// IsGate — есть ли у сегмента решётка. Таких два вида: замковые ворота и
// деревянные створки инженеров.
func (k SegmentKind) IsGate() bool { return k == SegGate || k == SegWoodGate }

func (k SegmentKind) ru() string {
	switch k {
	case SegGate:
		return "ворота"
	case SegWoodGate:
		return "деревянные ворота"
	case SegTower:
		return "башню"
	}
	return "стену"
}

// WallSegment — один участок стены. Разрушенный (HP<=0) становится проломом.
type WallSegment struct {
	Col   int         `json:"col"`
	Kind  SegmentKind `json:"kind"`
	HP    int         `json:"hp"`
	MaxHP int         `json:"maxHp"`

	// Open — поднята ли решётка. Только у ворот: пока она опущена, в проём
	// не войдёт никто, включая хозяина; поднята — ходят все.
	Open bool `json:"open,omitempty"`
}

func (s WallSegment) Breached() bool { return s.HP <= 0 }

// Passable — пускает ли сегмент кого-нибудь сквозь себя. Пролом проходим
// всегда, ворота — пока решётка поднята, кладка — никогда.
func (s WallSegment) Passable() bool {
	return s.Breached() || (s.Kind.IsGate() && s.Open)
}

// SegmentCount — число участков стены.
const SegmentCount = WallLastCol - WallFirstCol + 1

// towerCols — столбцы, где стена утолщается в башню.
var towerCols = map[int]bool{
	WallFirstCol: true, WallFirstCol + 5: true,
	WallLastCol - 5: true, WallLastCol: true,
}

func segmentIndex(col int) int { return col - WallFirstCol }

// IsCastleGate — столбец замковых ворот. Деревянную створку на их месте не
// ставят: там и так ворота, и подменять их фанерой нечестно по отношению к
// тарану, который именно за ними и приехал.
func IsCastleGate(col int) bool { return col == GateColLeft || col == GateColRight }

// originalSegment — каким сегмент был в начале осады. По нему отстраивают
// пролом: на месте замковых ворот поднимаются ворота, а не деревянная
// створка, которой этот пролом заколачивали до того.
func originalSegment(col int) (SegmentKind, int) {
	switch {
	case IsCastleGate(col):
		return SegGate, GateHP
	case towerCols[col]:
		return SegTower, TowerHP
	}
	return SegWall, WallHP
}

func newWall() []WallSegment {
	segs := make([]WallSegment, 0, SegmentCount)
	for col := WallFirstCol; col <= WallLastCol; col++ {
		kind, hp := originalSegment(col)
		segs = append(segs, WallSegment{Col: col, Kind: kind, HP: hp, MaxHP: hp})
	}
	return segs
}

// neighborOffsets — восемь направлений: юниты ходят по квадрату, диагональ
// стоит столько же, сколько прямой шаг.
var neighborOffsets = [8][2]int{
	{0, -1}, {0, 1}, {-1, 0}, {1, 0},
	{-1, -1}, {1, -1}, {-1, 1}, {1, 1},
}
