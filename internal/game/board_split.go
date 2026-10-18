package game

// Карта «Разделённый замок»: замок посреди поля, осаждающие в противоположных
// углах, река сквозь двор, форпост у истока.
//
// Карта и все числа ниже посчитаны заранее и проверены: симметрия поворотом
// на пол-оборота вокруг замка (мир одного осаждающего — точная копия мира
// другого), берега расходятся без переправ, до ближайших ворот от обоих
// лагерей поровну. Править её руками здесь не стоит — проще пересчитать.
//
// Почему симметрия поворотом, а не зеркалом, как на «Теснине»: в координатах
// вдоль оси (u поперёк течения, v вдоль) зеркало переводит (u,v) в (-u,v) и
// потому требует строго прямого русла. Поворот переводит (u,v) в (-u,-v) и
// допускает излучину — если каждый изгиб к северо-западу отвечает такому же
// изгибу к юго-востоку.
//
// Обозначения строк: . поле, B кусты, V обрыв, R скалы, M рудник, F пашня,
// D ров, ~ река, C двор, W боевой ход, # стена, T башня, G ворота,
// g водяные ворота (арка), a/b лагеря осаждающих.
var splitRows = []string{
	"aaaaaaaaaa...............................R~~",
	"aaaaaaaaa...VV........RRRRR..........~~~~~~~",
	"aaaaaaaa...V............R..........~~~~~~~~~",
	"aaaaaaa....VVV.........RR.........~~~~~~~R..",
	"aaaaaa...VVVVBBBB.....FR.RR.F....~~~......R.",
	"aaaaa....BV.VBBB.......R.........~~....R....",
	"aaaa...B..B....BB.B...B..B.....M~~~.........",
	"aaa...BBBB...BB.BBB....BB....B.B~~~.........",
	"aa....B...........BB..BBB...BBBB~~~.........",
	"a..V.VBB..BB....MBBBBB.BB....BB~~~.....V....",
	".VVV.....B.B....BBB..B.......~~~~~..........",
	".V.VVBB..BBB.....B.B.B...BB~~~~~B...........",
	"...V........DDDDDDDDDDDDDD~~~~DD.BB.........",
	"...VVB.B....DT#T##T#TGGT#Tggg#TD....B.......",
	"....BB......D#WWWWWWWWWWW~~~WW#D..B.B.......",
	"...BBBBB....DTWCCCCCCCCCC~~~CWTDB...........",
	"....B..BBM..D#WCCCCCCCCCC~~~CW#D............",
	".......B.BBBD#WCCCCCCCCCC~~CCW#DB.....R.....",
	"........B.BBDTWCCCCCCCCC~~~CCWTDBB.B......R.",
	".........B.BD#WCCCCCCCC~~~CCCW#D...BBBR.RR..",
	"......B.BB..DTWCCMCCCCCC~CCCCWTD..BB.BRRR...",
	"......B.....DGWCCCCCCCCCCCCCCWGD.BBBB..FR.R.",
	".R.RF..BBBB.DGWCCCCCCCCCCCCCCWGD.....B......",
	"...RRRB.BB..DTWCCCC~CCCCCCMCCWTD..BB.B......",
	"..RR.RBBB...D#WCCC~~~CCCCCCCCW#DB.B.........",
	".R......B.BBDTWCC~~~CCCCCCCCCWTDBB.B........",
	".....R.....BD#WCC~~CCCCCCCCCCW#DBBB.B.......",
	"............D#WC~~~CCCCCCCCCCW#D..MBB..B....",
	"...........BDTWC~~~CCCCCCCCCCWTD....BBBBB...",
	".......B.B..D#WW~~~WWWWWWWWWWW#D......BB....",
	".......B....DT#gggT#TGGT#T##T#TD....B.BVV...",
	".........BB.DD~~~~DDDDDDDDDDDDDD........V...",
	"...........B~~~~~BB...B.B.B.....BBB..BBVV.V.",
	"..........~~~~~.......B..BBB....B.B.....VVV.",
	"....V.....~~~BB....BB.BBBBBM....BB..BBV.V..b",
	".........~~~BBBB...BBB..BB...........B....bb",
	".........~~~B.B....BB....BBB.BB...BBBB...bbb",
	".........~~~M.....B..B...B.BB....B..B...bbbb",
	"....R....~~.........R.......BBBV.VB....bbbbb",
	".R...R..~~~....F.RR.RF.....BBBBVVVV...bbbbbb",
	"..R~~~~~~~.........RR.........VVV....bbbbbbb",
	"~~~~~~~~~..........R............V...bbbbbbbb",
	"~~~~~~~..........RRRRR........VV...bbbbbbbbb",
	"~~R...............................bbbbbbbbbb",
}

// Настилы, стоящие с первого дня: мосты у четырёх ворот и две переправы в
// поле. Форпост у истока — отдельно: он прочнее и он же запруда.
var splitDecks = []Point{{32, 7}, {33, 7}, {34, 7}, {21, 12}, {22, 12}, {25, 14}, {26, 14}, {27, 14}, {12, 21}, {31, 21}, {12, 22}, {31, 22}, {16, 29}, {17, 29}, {18, 29}, {21, 31}, {22, 31}, {9, 36}, {10, 36}, {11, 36}}

var splitFort = []Point{{38, 0}, {38, 1}, {38, 2}, {38, 3}, {38, 4}}

var Split = buildSplit()

func buildSplit() *Board {
	b := &Board{
		Key: "split", Name: "Разделённый замок",
		Cols: 44, Rows: 44,
		segAt: map[Point]int{}, segKind: map[Point]SegmentKind{},
		inside: map[Point]bool{}, campOf: map[Point]Side{},
		workshops: map[Side]Point{},
	}
	b.tiles = make([]TileKind, b.Cols*b.Rows)
	kinds := map[byte]TileKind{
		'.': TileField, 'B': TileBush, 'V': TileRavine, 'R': TileCliff,
		'M': TileMine, 'F': TileFarm, 'D': TileMoat, '~': TileRiver,
		'C': TileCourtyard, 'W': TileWalk, 'a': TileCamp, 'b': TileCamp,
		'#': TileWall, 'T': TileWall, 'G': TileWall, 'g': TileWall,
	}
	segKind := map[byte]SegmentKind{
		'#': SegWall, 'T': SegTower, 'G': SegGate, 'g': SegArch,
	}
	for y := 0; y < b.Rows; y++ {
		for x := 0; x < b.Cols; x++ {
			p := Point{x, y}
			ch := splitRows[y][x]
			b.tiles[y*b.Cols+x] = kinds[ch]
			switch ch {
			case 'a':
				b.campOf[p] = SideAttackerA
			case 'b':
				b.campOf[p] = SideAttackerB
			}
			if k, ok := segKind[ch]; ok {
				b.segAt[p] = len(b.segs)
				b.segKind[p] = k
				b.segs = append(b.segs, p)
				if k == SegGate {
					b.gates = append(b.gates, p)
				}
			}
			if kinds[ch].Water() {
				b.water = append(b.water, p)
			}
		}
	}
	// Внутри замка — всё, что кольцо кладки обводит: двор, боевой ход и
	// вода под арками. Заливкой от трона: так в «нутро» не попадёт ни ров
	// снаружи, ни река выше по течению.
	b.throne = Point{21, 21}
	b.floodInside(b.throne)
	for i, p := range b.segs {
		_ = i
		b.pitch = append(b.pitch, b.outward(p))
	}
	b.mines = []Point{{31, 6}, {16, 9}, {9, 16}, {34, 27}, {27, 34}, {12, 37}, {17, 20}, {26, 23}}
	b.farms = []Point{{22, 4}, {28, 4}, {39, 21}, {4, 22}, {15, 39}, {21, 39}}
	b.decks = splitDecks
	b.fort = splitFort
	// Казармы по одной в каждой половине двора: половины живут порознь, и
	// найм должен быть у каждой свой.
	b.barracks = []Point{{17, 17}, {26, 26}}
	b.workshops[SideAttackerA] = Point{3, 3}
	b.workshops[SideAttackerB] = Point{40, 40}
	// Стартовый гарнизон — по отряду за каждыми воротами.
	b.garrison = []Point{{21, 14}, {29, 21}, {22, 29}, {14, 22}}
	return b
}

// floodInside размечает нутро замка заливкой от трона: двор, боевой ход и
// вода, которую обводит кладка. Сквозь кладку заливка не идёт, поэтому ров
// снаружи и река выше по течению внутрь не попадают.
func (b *Board) floodInside(from Point) {
	queue := []Point{from}
	b.inside[from] = true
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range neighborOffsets {
			n := p.Add(d[0], d[1])
			if !b.In(n) || b.inside[n] || b.TileAt(n) == TileWall {
				continue
			}
			switch b.TileAt(n) {
			case TileCourtyard, TileWalk, TileMoat, TileRiver, TileMine, TileField:
				b.inside[n] = true
				queue = append(queue, n)
			}
		}
	}
}

// outward — клетка под сегментом снаружи: туда льют смолу. Сторона считается
// от середины замка, поэтому на кольце это работает так же, как на прямой
// стене «Теснины».
func (b *Board) outward(p Point) Point {
	cx, cy := float64(b.Cols-1)/2, float64(b.Rows-1)/2
	sx, sy := signf(float64(p.X)-cx), signf(float64(p.Y)-cy)
	for _, d := range [][2]int{{sx, 0}, {0, sy}, {sx, sy}} {
		n := p.Add(d[0], d[1])
		if b.In(n) && b.TileAt(n) != TileWall && !b.inside[n] {
			return n
		}
	}
	return p
}

func signf(v float64) int {
	if v > 0 {
		return 1
	}
	if v < 0 {
		return -1
	}
	return 0
}
