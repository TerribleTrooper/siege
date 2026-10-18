package game

// Туман войны. Видимость считается здесь и применяется только при сборке
// снимка (view.go): сама партия знает всё, а каждый игрок получает ровно то,
// что просматривают его отряды, постройки и удерживаемые башни — а у
// осаждающих ещё и отряды союзника по осаде.

// VisibleFor — множество клеток, которые сторона видит прямо сейчас.
// Для наблюдателя (SideNeutral) видно всё: он вне игры.
//
// Осаждающие смотрят **общими глазами**: по правилам осады они союзники,
// лагеря стоят рядом, разъезды пересекаются — что разведал один, знает и
// второй. Туман у них при этом остаётся: поле всё так же надо просматривать,
// просто делают они это вдвоём. Союз от этого не становится безопасным —
// приз всё равно достанется кому-то одному: короля убивает кто-то один.
func (s *State) VisibleFor(side Side) map[Point]bool {
	vis := map[Point]bool{}
	if side == SideNeutral {
		for y := 0; y < s.b.Rows; y++ {
			for x := 0; x < s.b.Cols; x++ {
				vis[Point{x, y}] = true
			}
		}
		return vis
	}

	// Чьими глазами смотрим. У замка — только своими.
	eyes := []Side{side}
	if side.IsAttacker() {
		eyes = []Side{SideAttackerA, SideAttackerB}
	}
	watching := func(sd Side) bool {
		for _, e := range eyes {
			if e == sd {
				return true
			}
		}
		return false
	}

	// Свой тыл виден всегда: замок изнутри и оба лагеря осаждающих.
	for y := 0; y < s.b.Rows; y++ {
		for x := 0; x < s.b.Cols; x++ {
			p := Point{x, y}
			if side == SideDefender && (s.b.IsInsideCastle(p) || s.b.TileAt(p) == TileWall) {
				vis[p] = true
			}
			if side.IsAttacker() && s.b.CampSide(p) != SideNeutral {
				vis[p] = true
			}
		}
	}

	for _, u := range s.Units {
		// Пассажир осадной башни смотрит в доски: наружу глядит сама башня.
		if !watching(u.Owner) || u.Carrier != 0 {
			continue
		}
		addSquare(vis, u.Pos, s.unitVision(u))
	}

	for _, b := range s.Buildings {
		if !watching(s.towerOwner(b)) {
			continue
		}
		addSquare(vis, b.Pos, buildingVision(b.Kind))
	}
	return vis
}

func addSquare(vis map[Point]bool, centre Point, r int) {
	if r <= 0 {
		vis[centre] = true
		return
	}
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			p := centre.Add(dx, dy)
			if p.InBounds() {
				vis[p] = true
			}
		}
	}
}

// CanSee — видит ли сторона конкретную клетку.
func (s *State) CanSee(side Side, p Point) bool {
	return s.VisibleFor(side)[p]
}

// fogRows кодирует видимость построчно: '1' — видно, '0' — туман.
// Так маска занимает в снимке несколько сотен байт вместо списка точек.
func (s *State) fogRows(vis map[Point]bool) []string {
	out := make([]string, 0, s.b.Rows)
	for y := 0; y < s.b.Rows; y++ {
		row := make([]byte, s.b.Cols)
		for x := 0; x < s.b.Cols; x++ {
			row[x] = '0'
			if vis[Point{x, y}] {
				row[x] = '1'
			}
		}
		out = append(out, string(row))
	}
	return out
}
