package game

import "fmt"

// Геометрия карты и всё, что связано с проходимостью.
//
// Карт в игре две, и обе описываются одним типом Board: классическая
// «Теснина» — замок поперёк ущелья, лагеря внизу — и «Разделённый замок» —
// замок посреди поля, лагеря в противоположных углах, река сквозь двор.
// Раньше геометрия жила в константах (WallRow, GateColLeft и прочие), и
// карта могла быть только одна; теперь константы описывают **классическую**
// доску, а всё остальное спрашивает у Board.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func (p Point) Add(dx, dy int) Point { return Point{p.X + dx, p.Y + dy} }

// InBounds — внутри ли точка классической карты. Оставлено для кода и тестов,
// которые работают только с ней; всё, что знает свою доску, спрашивает
// Board.In.
func (p Point) InBounds() bool { return Classic.In(p) }

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
	// TileRiver — река. Для правил это та же вода, что и ров: не переходит
	// никто, настил кладётся теми же фашинами. Отдельный вид нужен, чтобы
	// карточка клетки не называла реку рвом, а клиент рисовал течение.
	TileRiver
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
	TileRiver:     "river",
}

func (k TileKind) String() string { return tileKindNames[k] }

// Water — вода ли это: ров или река. В воду не входит никто, и засыпают её
// одинаково, поэтому правила спрашивают именно так, а не сравнивают с
// конкретным видом клетки.
func (k TileKind) Water() bool { return k == TileMoat || k == TileRiver }

// Board — карта осады: рельеф, кольцо кладки, лагеря и опорные точки
// расстановки. Создаётся один раз при старте программы и дальше только
// читается, поэтому делить её между партиями безопасно.
type Board struct {
	Key  string // как карта называется в протоколе
	Name string // и как её видит игрок
	Cols int
	Rows int

	tiles []TileKind // рельеф, строка за строкой

	// Кладка. Сегменты хранятся в порядке обхода кольца, и каждый знает
	// свою клетку: на классической карте стена — строка, на второй — кольцо,
	// и привязка к столбцу больше не работает.
	segs    []Point
	segAt   map[Point]int
	segKind map[Point]SegmentKind

	throne Point
	inside map[Point]bool // всё, что внутри кладки: двор, ход, вода под арками
	campOf map[Point]Side

	mines []Point
	farms []Point
	water []Point // ров и река: то, что засыпают фашинами
	gates []Point
	pitch []Point // куда замок льёт смолу — клетка перед каждым сегментом

	// Стартовая расстановка. Карта знает её сама: иначе вторую карту
	// пришлось бы вписывать в startingBuildings условиями.
	barracks  []Point
	workshops map[Side]Point
	decks     []Point // мосты, стоящие с первого дня
	fort      []Point // форпост у истока реки, если он на карте есть
	garrison  []Point // где стоит стартовый гарнизон замка
}

// In — внутри ли точка этой карты.
func (b *Board) In(p Point) bool {
	return p.X >= 0 && p.X < b.Cols && p.Y >= 0 && p.Y < b.Rows
}

// TileAt — статический тип клетки, без учёта проломов, построек и юнитов.
func (b *Board) TileAt(p Point) TileKind {
	if !b.In(p) {
		return TileField
	}
	return b.tiles[p.Y*b.Cols+p.X]
}

// IsInsideCastle — клетки, куда осаждающий попадает только через пролом.
// Они же — весь мир короля: за эту черту он не выходит. Вода под аркой
// тоже внутри: она за кладкой, просто по ней не ходят.
func (b *Board) IsInsideCastle(p Point) bool { return b.inside[p] }

// CampSide — какому осаждающему принадлежит клетка лагеря.
func (b *Board) CampSide(p Point) Side {
	if sd, ok := b.campOf[p]; ok {
		return sd
	}
	return SideNeutral
}

// ThroneTile — где король стоит в начале осады. Дальше он ходит сам.
func (b *Board) ThroneTile() Point { return b.throne }

func (b *Board) IsMine(p Point) bool  { return b.TileAt(p) == TileMine }
func (b *Board) IsFarm(p Point) bool  { return b.TileAt(p) == TileFarm }
func (b *Board) IsWater(p Point) bool { return b.TileAt(p).Water() }
func (b *Board) InBush(p Point) bool  { return b.TileAt(p) == TileBush }

// MoveCost — во сколько обходится вход в клетку по рельефу. Вода сюда не
// входит: засыпана она или нет, знает только State (см. State.moveCost).
func (b *Board) MoveCost(p Point) int {
	if b.TileAt(p) == TileBush {
		return BushMoveCost
	}
	return 1
}

func (b *Board) MineTiles() []Point  { return b.mines }
func (b *Board) FarmTiles() []Point  { return b.farms }
func (b *Board) WaterTiles() []Point { return b.water }
func (b *Board) GateTiles() []Point  { return b.gates }
func (b *Board) PitchTiles() []Point { return b.pitch }
func (b *Board) FortTiles() []Point  { return b.fort }

// Segments — сколько в кладке участков.
func (b *Board) Segments() int { return len(b.segs) }

// SegmentIndex — место сегмента в кольце, −1 если на этой клетке кладки нет.
func (b *Board) SegmentIndex(p Point) int {
	if i, ok := b.segAt[p]; ok {
		return i
	}
	return -1
}

// SegmentPos — клетка сегмента по его номеру.
func (b *Board) SegmentPos(i int) Point { return b.segs[i] }

// IsCastleGate — замковые ворота. Деревянную створку на их месте не ставят:
// там и так ворота, и подменять их фанерой нечестно по отношению к тарану,
// который именно за ними и приехал.
func (b *Board) IsCastleGate(p Point) bool { return b.segKind[p] == SegGate }

// originalSegment — каким сегмент был в начале осады. По нему отстраивают
// пролом: на месте замковых ворот поднимаются ворота, а не деревянная
// створка, которой этот пролом заколачивали до того.
func (b *Board) originalSegment(p Point) (SegmentKind, int) {
	kind := b.segKind[p]
	return kind, segmentHP(kind)
}

func segmentHP(kind SegmentKind) int {
	switch kind {
	case SegGate:
		return GateHP
	case SegTower:
		return TowerHP
	case SegArch:
		return ArchHP
	case SegWoodGate:
		return WoodGateHP
	}
	return WallHP
}

// newWall — кладка к первому дню осады.
func (b *Board) newWall() []WallSegment {
	segs := make([]WallSegment, 0, len(b.segs))
	for _, p := range b.segs {
		kind, hp := b.originalSegment(p)
		segs = append(segs, WallSegment{Pos: p, Kind: kind, HP: hp, MaxHP: hp})
	}
	return segs
}

// --- совместимость: всё, что ниже, говорит о классической карте ---
//
// Тесты и код, которому доска не нужна, продолжают звать эти функции. Внутри
// они спрашивают Classic, так что поведение прежнее.

func TileAt(p Point) TileKind     { return Classic.TileAt(p) }
func CampSide(p Point) Side       { return Classic.CampSide(p) }
func IsInsideCastle(p Point) bool { return Classic.IsInsideCastle(p) }
func ThroneTile() Point           { return Classic.ThroneTile() }
func IsMine(p Point) bool         { return Classic.IsMine(p) }
func IsFarm(p Point) bool         { return Classic.IsFarm(p) }
func IsMoat(p Point) bool         { return Classic.IsWater(p) }
func InBush(p Point) bool         { return Classic.InBush(p) }
func MoveCost(p Point) int        { return Classic.MoveCost(p) }
func MineTiles() []Point          { return Classic.MineTiles() }
func FarmTiles() []Point          { return Classic.FarmTiles() }
func MoatTiles() []Point          { return Classic.WaterTiles() }
func CastleMine() Point           { return Point{CastleMineCol, CastleMineRow} }
func IsCastleGate(col int) bool   { return col == GateColLeft || col == GateColRight }
func segmentIndex(col int) int    { return col - WallFirstCol }

// SegmentKind различает участки стены.
type SegmentKind uint8

const (
	SegWall SegmentKind = iota
	SegGate
	SegTower
	// SegWoodGate — деревянные ворота, которыми инженеры заколачивают пролом.
	// Такие же створки с решёткой, только прочности в них вчетверо меньше.
	SegWoodGate
	// SegArch — водяные ворота: арка, под которой река входит в замок и
	// выходит из него. Решётки в ней нет, зато есть вода: пролом в арке
	// впускает в замок по реке, минуя ров.
	SegArch
)

var segmentKindNames = map[SegmentKind]string{
	SegWall: "wall", SegGate: "gate", SegTower: "tower",
	SegWoodGate: "woodgate", SegArch: "arch",
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
// деревянные створки инженеров. У арки решётки нет: там течёт река.
func (k SegmentKind) IsGate() bool { return k == SegGate || k == SegWoodGate }

func (k SegmentKind) ru() string {
	switch k {
	case SegGate:
		return "ворота"
	case SegWoodGate:
		return "деревянные ворота"
	case SegTower:
		return "башню"
	case SegArch:
		return "водяные ворота"
	}
	return "стену"
}

// WallSegment — один участок стены. Разрушенный (HP<=0) становится проломом.
type WallSegment struct {
	Pos   Point       `json:"pos"`
	Kind  SegmentKind `json:"kind"`
	HP    int         `json:"hp"`
	MaxHP int         `json:"maxHp"`

	// Open — поднята ли решётка. Только у ворот: пока она опущена, в проём
	// не войдёт никто, включая хозяина; поднята — ходят все.
	Open bool `json:"open,omitempty"`
}

// Col — столбец сегмента. Протокол классической карты ссылается на кладку
// столбцом, и ради него поле осталось; на карте с кольцом стены смысла в нём
// нет, и там действия адресуют сегмент клеткой.
func (s WallSegment) Col() int { return s.Pos.X }

func (s WallSegment) Breached() bool { return s.HP <= 0 }

// Passable — пускает ли сегмент кого-нибудь сквозь себя. Пролом проходим
// всегда, ворота — пока решётка поднята, кладка — никогда. Под аркой течёт
// река: пролом в ней открывает дорогу, но дорога эта — по воде.
func (s WallSegment) Passable() bool {
	return s.Breached() || (s.Kind.IsGate() && s.Open)
}

// neighborOffsets — восемь направлений: юниты ходят по квадрату, диагональ
// стоит столько же, сколько прямой шаг.
var neighborOffsets = [8][2]int{
	{0, -1}, {0, 1}, {-1, 0}, {1, 0},
	{-1, -1}, {1, -1}, {-1, 1}, {1, 1},
}

// Boards — все карты игры. Порядок — порядок показа в лобби.
func Boards() []*Board { return []*Board{Classic, Split} }

// BoardByKey — карта по ключу протокола. Неизвестный ключ — классическая:
// партия должна начаться даже с кривым запросом.
func BoardByKey(key string) *Board {
	for _, b := range Boards() {
		if b.Key == key {
			return b
		}
	}
	return Classic
}

// setTile подменяет клетку рельефа. Нужно только тестам: в игре карта не
// меняется за всю партию, и менять её на ходу некому.
func (b *Board) setTile(p Point, k TileKind) {
	if b.In(p) {
		b.tiles[p.Y*b.Cols+p.X] = k
	}
}

// walkBehind — клетка боевого хода за этим сегментом: туда поднимаются по
// лестнице и по трапу осадной башни. На «Теснине» это всегда клетка выше,
// на кольце — та из соседних, что смотрит внутрь замка.
func (b *Board) walkBehind(wall Point) (Point, bool) {
	for _, d := range [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
		n := wall.Add(d[0], d[1])
		if b.inside[n] && b.TileAt(n) == TileWalk {
			return n, true
		}
	}
	return Point{}, false
}

// wallDistance — сколько клеток до ближайшего участка кладки. По нему
// считается запрет ставить аванпост под самой стеной: на кольце «ряда стены»
// нет, а расстояние есть всегда.
func (b *Board) wallDistance(p Point) int {
	best := b.Cols + b.Rows
	for _, w := range b.segs {
		if d := Chebyshev(p, w); d < best {
			best = d
		}
	}
	return best
}

// InsideTiles — все клетки внутри кладки. По ним замок строит и созывает
// ополчение, и порядок здесь устойчивый: сверху вниз, слева направо.
func (b *Board) InsideTiles() []Point {
	out := make([]Point, 0, len(b.inside))
	for y := 0; y < b.Rows; y++ {
		for x := 0; x < b.Cols; x++ {
			if p := (Point{x, y}); b.inside[p] {
				out = append(out, p)
			}
		}
	}
	return out
}

// WallDistance — сколько клеток до ближайшего участка кладки.
func (b *Board) WallDistance(p Point) int { return b.wallDistance(p) }

// WalkBehind — клетка боевого хода за этим сегментом.
func (b *Board) WalkBehind(wall Point) (Point, bool) { return b.walkBehind(wall) }

// OutsideOf — клетка снаружи, вплотную к сегменту: туда льют смолу, оттуда
// подходят к стене.
func (b *Board) OutsideOf(wall Point) Point {
	if i := b.SegmentIndex(wall); i >= 0 && i < len(b.pitch) {
		return b.pitch[i]
	}
	return wall
}

// StageTile — клетка в steps шагах от сегмента наружу: там осада собирает
// штурмовой отряд, подальше от стрелков со стены.
func (b *Board) StageTile(wall Point, steps int) Point {
	out := b.OutsideOf(wall)
	dx, dy := out.X-wall.X, out.Y-wall.Y
	p := Point{wall.X + dx*steps, wall.Y + dy*steps}
	for !b.In(p) && steps > 1 {
		steps--
		p = Point{wall.X + dx*steps, wall.Y + dy*steps}
	}
	return p
}

// CampCentre — середина лагеря стороны: оттуда осада выходит, и по ней
// считается, какой участок стены ей ближе.
func (b *Board) CampCentre(side Side) Point {
	sx, sy, n := 0, 0, 0
	for p, sd := range b.campOf {
		if sd == side {
			sx, sy, n = sx+p.X, sy+p.Y, n+1
		}
	}
	if n == 0 {
		return Point{b.Cols / 2, b.Rows / 2}
	}
	return Point{sx / n, sy / n}
}

// CastleMines — клетки рудника внутри замка. На «Теснине» она одна, в углу
// двора; на «Разделённом замке» их две, по одной на половину.
func (b *Board) CastleMines() []Point {
	var out []Point
	for _, p := range b.mines {
		if b.inside[p] {
			out = append(out, p)
		}
	}
	return out
}
