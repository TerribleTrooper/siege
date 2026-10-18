package game

// Классическая карта «Теснина»: замок поперёк ущелья, лагеря осаждающих
// внизу, ров вдоль стены.
//
//	y=0..2  двор: король, казармы, стрелковые башни, рудник в углу
//	y=3     боевой ход: здесь стоят защитники
//	y=4     линия стены: сегменты, башни, ворота
//	y=5     ров с водой: не переходит никто, только по настилу
//	y=6..19 поле: кусты, обрывы, рудник
//	y=20,21 лагеря осаждающих
//
// Доска собирается из тех же констант, что описывали карту раньше, поэтому
// партия на ней идёт ровно так же, как до появления второй карты.
var Classic = buildClassic()

func buildClassic() *Board {
	b := &Board{
		Key: "classic", Name: "Теснина",
		Cols: Cols, Rows: Rows,
		segAt: map[Point]int{}, segKind: map[Point]SegmentKind{},
		inside: map[Point]bool{}, campOf: map[Point]Side{},
		workshops: map[Side]Point{},
	}
	b.tiles = make([]TileKind, b.Cols*b.Rows)
	terrain := classicTerrain()
	for y := 0; y < b.Rows; y++ {
		for x := 0; x < b.Cols; x++ {
			b.tiles[y*b.Cols+x] = classicTile(Point{x, y}, terrain)
		}
	}

	for col := WallFirstCol; col <= WallLastCol; col++ {
		p := Point{col, WallRow}
		kind := SegWall
		switch {
		case col == GateColLeft || col == GateColRight:
			kind = SegGate
		case classicTower(col):
			kind = SegTower
		}
		b.segAt[p] = len(b.segs)
		b.segKind[p] = kind
		b.segs = append(b.segs, p)
		// Со стены льют вниз, под стену, а не себе под ноги в кладку.
		b.pitch = append(b.pitch, Point{col, WallRow + 1})
		if kind == SegGate {
			b.gates = append(b.gates, p)
			b.decks = append(b.decks, Point{col, MoatRow})
		}
		b.water = append(b.water, Point{col, MoatRow})
	}

	for y := 0; y < b.Rows; y++ {
		for x := 0; x < b.Cols; x++ {
			p := Point{x, y}
			switch b.TileAt(p) {
			case TileWalk, TileCourtyard:
				b.inside[p] = true
			case TileMine:
				b.mines = append(b.mines, p)
			case TileFarm:
				b.farms = append(b.farms, p)
			case TileCamp:
				if x < CampWidth {
					b.campOf[p] = SideAttackerA
				} else {
					b.campOf[p] = SideAttackerB
				}
			}
		}
	}
	// Порядок клеток рудника тот же, что отдавал MineTiles: две в поле,
	// потом угловая во дворе. По нему расставлены подписи в интерфейсе.
	b.mines = []Point{
		{MineColLeft, MineRow - MineGap},
		{MineColRight, MineRow + MineGap},
		{CastleMineCol, CastleMineRow},
	}
	b.farms = []Point{{FarmColLeft, FarmRow}, {Cols - 1 - FarmColLeft, FarmRow}}

	b.throne = Point{GateColLeft, ThroneRow}
	b.barracks = []Point{
		{WallFirstCol + 2, CourtRowFirst + 1},
		{WallLastCol - 2, CourtRowFirst + 1},
	}
	b.workshops[SideAttackerA] = Point{3, CampRowLast}
	b.workshops[SideAttackerB] = Point{Cols - 4, CampRowLast}
	b.garrison = []Point{
		{GateColLeft, WalkRow}, {GateColRight, WalkRow},
		{WallFirstCol + 3, WalkRow}, {WallLastCol - 3, WalkRow},
	}
	return b
}

func classicTower(col int) bool {
	switch col {
	case WallFirstCol, WallFirstCol + 5, WallLastCol - 5, WallLastCol:
		return true
	}
	return false
}

// classicTile — прежний TileAt слово в слово: порядок проверок важен, скалы
// считаются раньше двора, а рудник во дворе — раньше самого двора.
func classicTile(p Point, terrain map[Point]TileKind) TileKind {
	switch {
	case p.X < 0 || p.X >= Cols || p.Y < 0 || p.Y >= Rows:
		return TileField
	case p.Y <= WallRow && (p.X < WallFirstCol || p.X > WallLastCol):
		return TileCliff // замок стоит в теснине: фланги непроходимы
	case p == (Point{CastleMineCol, CastleMineRow}):
		return TileMine // рудник в углу двора: проверяем до самого двора
	case p.Y >= CourtRowFirst && p.Y <= CourtRowLast:
		return TileCourtyard
	case p.Y == WalkRow:
		return TileWalk
	case p.Y == WallRow:
		return TileWall
	case p.Y == MoatRow && p.X >= WallFirstCol && p.X <= WallLastCol:
		return TileMoat
	case p.Y >= CampRowFirst && (p.X < CampWidth || p.X >= Cols-CampWidth):
		return TileCamp
	}
	if k, ok := terrain[p]; ok {
		return k
	}
	return TileField
}

// classicTerrain — рельеф поля. Карта зеркальна относительно вертикальной
// оси (x ↔ Cols-1-x): у обоих осаждающих одинаковые подходы, иначе гонка за
// пролом была бы нечестной.
func classicTerrain() map[Point]TileKind {
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

	// Полевые рудники: северный ближе к замку, южный — к лагерям.
	m[Point{MineColLeft, MineRow - MineGap}] = TileMine
	m[Point{MineColRight, MineRow + MineGap}] = TileMine
	// Пашни по флангам: стоять на них невыгодно позиционно (край карты, до
	// центра далеко), зато обоз подвозит припасы каждый ход.
	m[Point{FarmColLeft, FarmRow}] = TileFarm
	m[Point{Cols - 1 - FarmColLeft, FarmRow}] = TileFarm
	return m
}
