package game

import "testing"

// Вторая карта: замок посреди поля, осаждающие в противоположных углах,
// река сквозь двор, форпост у истока. Проверяем не красоту, а то, на чём
// партия ломается: симметрию, проходимость и то, что кольцо стены — кольцо.

func TestSplitBoardShape(t *testing.T) {
	b := Split
	if b.Cols != 44 || b.Rows != 44 {
		t.Fatalf("карта %dx%d, ждали 44x44", b.Cols, b.Rows)
	}
	gates, arches, towers := 0, 0, 0
	for _, p := range b.segs {
		switch b.segKind[p] {
		case SegGate:
			gates++
		case SegArch:
			arches++
		case SegTower:
			towers++
		}
	}
	if b.Segments() < 60 {
		t.Fatalf("кладки всего %d сегментов — это не кольцо", b.Segments())
	}
	if gates != 8 {
		t.Fatalf("ворот %d клеток, ждали 8 (четверо ворот по две клетки)", gates)
	}
	if arches == 0 {
		t.Fatal("река входит в замок без арок: водяных ворот нет")
	}
	if towers == 0 {
		t.Fatal("башен на кольце нет")
	}
	if !b.IsInsideCastle(b.ThroneTile()) {
		t.Fatalf("трон %v оказался вне замка", b.ThroneTile())
	}
	if len(b.fort) == 0 {
		t.Fatal("форпоста у истока нет")
	}
	// Форпост стоит поперёк ручья: иначе он не переправа, а сарай на берегу.
	water := 0
	for _, p := range b.fort {
		if b.TileAt(p).Water() {
			water++
		}
	}
	if water == 0 {
		t.Fatal("форпост не стоит на воде — он должен перекрывать ручей")
	}
}

// Симметрия поворотом: мир одного осаждающего — точная копия мира другого,
// повёрнутая на пол-оборота вокруг замка. Форпост из проверки исключён: он
// на карте один, и честность его меряется ходами, а не отражением.
func TestSplitBoardIsSymmetric(t *testing.T) {
	b := Split
	fort := map[Point]bool{}
	for _, p := range b.fort {
		fort[p] = true
	}
	for y := 0; y < b.Rows; y++ {
		for x := 0; x < b.Cols; x++ {
			p := Point{x, y}
			q := Point{b.Cols - 1 - x, b.Rows - 1 - y}
			if fort[p] || fort[q] {
				continue
			}
			if b.TileAt(p) != b.TileAt(q) {
				t.Fatalf("несимметрично: %v=%s против %v=%s",
					p, b.TileAt(p), q, b.TileAt(q))
			}
			if a, c := b.CampSide(p), b.CampSide(q); a != SideNeutral || c != SideNeutral {
				if !(a == SideAttackerA && c == SideAttackerB || a == SideAttackerB && c == SideAttackerA) {
					t.Fatalf("лагеря несимметричны: %v=%s против %v=%s", p, a, q, c)
				}
			}
		}
	}
}

// Река должна быть преградой, а не рисунком: без переправ берега не
// сходятся, с ними — сходятся. Диагональный прыжок через русло тоже не
// проходит: один шаг меняет сумму координат не больше чем на два.
func TestSplitRiverSeparatesBanks(t *testing.T) {
	b := Split
	reach := func(decks bool) map[Point]bool {
		seen := map[Point]bool{}
		start := Point{2, 2}
		queue := []Point{start}
		seen[start] = true
		deck := map[Point]bool{}
		if decks {
			for _, p := range append(append([]Point{}, b.decks...), b.fort...) {
				deck[p] = true
			}
		}
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			for _, d := range neighborOffsets {
				n := p.Add(d[0], d[1])
				if !b.In(n) || seen[n] {
					continue
				}
				if b.TileAt(n).Water() && !deck[n] {
					continue
				}
				switch b.TileAt(n) {
				case TileCliff, TileRavine, TileWall:
					continue
				}
				seen[n] = true
				queue = append(queue, n)
			}
		}
		return seen
	}
	far := Point{b.Cols - 3, b.Rows - 3}
	if reach(false)[far] {
		t.Fatal("берега сходятся без переправ: русло дырявое")
	}
	if !reach(true)[far] {
		t.Fatal("с мостами и форпостом до чужого лагеря всё равно не добраться")
	}
}

// Осада на второй карте должна начинаться так же, как на первой: король на
// троне, гарнизон на стене, мосты и форпост на месте.
func TestSplitGameStarts(t *testing.T) {
	s := NewOn(Split, [3]string{"Аня", "Боря", "Витя"})
	king := s.King(SideDefender)
	if king == nil || king.Pos != Split.ThroneTile() {
		t.Fatalf("король не на троне: %v", king)
	}
	if len(s.Wall) != Split.Segments() {
		t.Fatalf("в кладке %d сегментов, а на карте %d", len(s.Wall), Split.Segments())
	}
	kinds := map[BuildingKind]int{}
	for _, b := range s.Buildings {
		kinds[b.Kind]++
	}
	if kinds[BuildBarracks] == 0 || kinds[BuildWorkshop] != 2 {
		t.Fatalf("стартовые постройки не на месте: %v", kinds)
	}
	if kinds[BuildFort] == 0 {
		t.Fatal("форпоста у истока нет среди построек")
	}
	if kinds[BuildBridge] == 0 {
		t.Fatal("мостов через ров нет: к воротам не подойти")
	}
	// Обе стороны должны уметь нанимать: защитник во дворе, осада в лагере.
	for _, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
		if len(s.deployTiles(side)) == 0 {
			t.Fatalf("%s негде выставлять отряды", side)
		}
	}
}

// Классическая карта от появления второй не изменилась ни на клетку.
func TestClassicBoardUnchanged(t *testing.T) {
	if Classic.Cols != Cols || Classic.Rows != Rows {
		t.Fatalf("размер «Теснины» уехал: %dx%d", Classic.Cols, Classic.Rows)
	}
	if Classic.Segments() != WallLastCol-WallFirstCol+1 {
		t.Fatalf("сегментов стены %d, ждали %d", Classic.Segments(), WallLastCol-WallFirstCol+1)
	}
	if Classic.ThroneTile() != (Point{GateColLeft, ThroneRow}) {
		t.Fatalf("трон уехал: %v", Classic.ThroneTile())
	}
	for _, p := range []Point{{0, 0}, {13, 4}, {13, 5}, {1, 12}, {13, 9}} {
		if Classic.TileAt(p) != TileAt(p) {
			t.Fatalf("клетка %v читается по-разному: %s и %s", p, Classic.TileAt(p), TileAt(p))
		}
	}
}
