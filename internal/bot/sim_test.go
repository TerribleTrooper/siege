package bot

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"siege/internal/game"
)

// simStats — что произошло за партию ботов: для проверок и для отладки.
type simStats struct {
	actions  int
	rejects  int
	stalls   int // ходы, которые пришлось завершить за бота
	result   *game.Result
	byKind   map[game.ActionKind]int
	rejected map[string]int
	elapsed  time.Duration
}

// simulate играет партию одними ботами — так же, как её ведёт комната:
// снимок для места, действие, проверка сервером. Место 0 держит замок,
// места 1 и 2 осаждают.
func simulate(t testing.TB, levels [3]Level, seed int64) (*game.State, simStats) {
	t.Helper()
	trace := os.Getenv("SIEGE_SIM_TRACE") != ""
	start := time.Now()
	s := game.New([3]string{"Аня", "Боря", "Витя"})
	var bots [3]*Bot
	for i := range bots {
		bots[i] = New(levels[i], seed+int64(i))
	}
	st := simStats{byKind: map[game.ActionKind]int{}, rejected: map[string]int{}}
	perTurn := 0
	for s.Phase == game.PhasePlay {
		seat := s.SeatOf(s.Turn)
		side := s.Turn
		v := s.ViewFor(seat)
		a := bots[seat].Next(&v)
		perTurn++
		if perTurn > 80 {
			st.stalls++
			a = game.Action{Kind: game.ActEndTurn}
		}
		before := len(s.Units)
		var kingHP int
		if k := s.King(game.SideDefender); k != nil {
			kingHP = k.HP
		}
		if err := s.Apply(side, a); err != nil {
			if a.Kind == game.ActEndTurn {
				t.Fatalf("завершение хода отбито: %v", err)
			}
			bots[seat].Reject(a)
			st.rejects++
			st.rejected[fmt.Sprintf("%s/%s%s%s: %v", side, a.Kind, a.Build, a.Ability, err)]++
			continue
		}
		st.actions++
		st.byKind[a.Kind]++
		if trace {
			note := ""
			switch {
			case a.Kind == game.ActCharge || a.Kind == game.ActAbility || a.Kind == game.ActConstruct || a.Kind == game.ActClimb:
				note = fmt.Sprintf("%s %s%s col=%d to=%v", a.Kind, a.Ability, a.Build, a.Col, a.To)
			case a.Kind == game.ActBuild && a.Build != "fascine":
				note = fmt.Sprintf("build %s to=%v", a.Build, a.To)
			case len(s.Units) < before:
				note = fmt.Sprintf("%s убил (%d→%d отрядов) to=%v", a.Kind, before, len(s.Units), a.To)
			}
			if k := s.King(game.SideDefender); k != nil && k.HP < kingHP {
				note += fmt.Sprintf(" КОРОЛЬ %d→%d", kingHP, k.HP)
			}
			if note != "" {
				t.Logf("д%d %s: %s", s.Day, side, note)
			}
			if a.Kind == game.ActEndTurn && side == game.SideAttackerB && s.Day%5 == 0 {
				t.Logf("— день %d: %s", s.Day-1, summary(s))
				if os.Getenv("SIEGE_SIM_BOARD") != "" {
					t.Log(board(s))
				}
			}
		}
		if a.Kind == game.ActEndTurn || s.Turn != side {
			perTurn = 0
		}
	}
	st.result = s.Result
	st.elapsed = time.Since(start)
	return s, st
}

// TestSimDebug — партия для глаз: SIEGE_SIM=1 go test ./internal/bot -run SimDebug -v
// Уровни по местам (замок, осада I, осада II): SIEGE_SIM_LEVEL=hard,normal,easy.
func TestSimDebug(t *testing.T) {
	if os.Getenv("SIEGE_SIM") == "" {
		t.Skip("отладочная партия: SIEGE_SIM=1")
	}
	levels := [3]Level{Normal, Normal, Normal}
	if raw := os.Getenv("SIEGE_SIM_LEVEL"); raw != "" {
		parts := strings.Split(raw, ",")
		for i := range levels {
			levels[i] = Level(strings.TrimSpace(parts[min(i, len(parts)-1)]))
		}
	}
	var seed int64 = 1
	fmt.Sscan(os.Getenv("SIEGE_SIM"), &seed)
	_, st := simulate(t, levels, seed)
	t.Logf("%v: действий %d, отбито %d, зависло %d, %v", levels, st.actions, st.rejects, st.stalls, st.elapsed)
	for k, n := range st.byKind {
		t.Logf("  %s: %d", k, n)
	}
	for k, n := range st.rejected {
		t.Logf("  отбито %dx: %s", n, k)
	}
	if r := st.result; r != nil {
		t.Logf("итог: %s (взят=%v кем=%s день %d)", r.Summary, r.Captured, r.CapturedBy, r.Days)
	}
}

// summary — коротко о положении: сколько у кого отрядов, припасов и где
// пробита стена.
func summary(s *game.State) string {
	var n [4]int
	types := map[game.Side]map[string]int{}
	for _, u := range s.Units {
		n[u.Owner]++
		if types[u.Owner] == nil {
			types[u.Owner] = map[string]int{}
		}
		types[u.Owner][u.Type.String()]++
	}
	wall := []byte{}
	for _, seg := range s.Wall {
		switch {
		case seg.HP <= 0:
			wall = append(wall, '_')
		case seg.HP < seg.MaxHP:
			wall = append(wall, '~')
		default:
			wall = append(wall, '#')
		}
	}
	k := s.King(game.SideDefender)
	kp := "-"
	if k != nil {
		kp = fmt.Sprintf("%v hp%d", k.Pos, k.HP)
	}
	return fmt.Sprintf("стена %s король %s | замок %d %v (пр %d) | A %d %v (пр %d, ср %d) | B %d %v (пр %d, ср %d)",
		wall, kp, n[game.SideDefender], types[game.SideDefender], s.Supplies(game.SideDefender),
		n[game.SideAttackerA], types[game.SideAttackerA], s.Supplies(game.SideAttackerA), s.Silver(game.SideAttackerA),
		n[game.SideAttackerB], types[game.SideAttackerB], s.Supplies(game.SideAttackerB), s.Silver(game.SideAttackerB))
}

// board — поле буквами: кто где стоит. Для отладки поведения ботов.
func board(s *game.State) string {
	glyph := map[game.UnitType]byte{
		game.UnitInfantry: 'i', game.UnitScout: 's', game.UnitLadder: 'l', game.UnitRam: 'r',
		game.UnitBomber: 'b', game.UnitCatapult: 'c', game.UnitGarrison: 'g', game.UnitBallista: 'a',
		game.UnitCrossbow: 'x', game.UnitKnight: 'k', game.UnitEngineer: 'e', game.UnitHealer: 'h',
		game.UnitKing: '@', game.UnitTrebuchet: 't', game.UnitSiegeTower: 'T', game.UnitTemplar: 'p',
		game.UnitMilitia: 'm', game.UnitSapper: 'q', game.UnitMantlet: 'n',
	}
	rows := make([][]byte, game.Rows)
	for y := range rows {
		rows[y] = make([]byte, game.Cols)
		for x := range rows[y] {
			p := game.Point{X: x, Y: y}
			c := byte('.')
			switch game.TileAt(p) {
			case game.TileCliff, game.TileRavine:
				c = ' '
			case game.TileBush:
				c = '"'
			case game.TileMoat:
				c = '~'
			case game.TileWall:
				c = '#'
				if s.Wall[x-game.WallFirstCol].HP <= 0 {
					c = '_'
				}
			case game.TileMine:
				c = '$'
			case game.TileFarm:
				c = 'F'
			}
			rows[y][x] = c
		}
	}
	for _, b := range s.Buildings {
		rows[b.Pos.Y][b.Pos.X] = 'B'
	}
	for _, u := range s.Units {
		if u.Carrier != 0 {
			continue
		}
		c := glyph[u.Type]
		if u.Owner == game.SideAttackerA || u.Owner == game.SideAttackerB {
			if u.Owner == game.SideAttackerB {
				c = c - 'a' + 'A'
				if c < 'A' || c > 'Z' {
					c = '?'
				}
			}
		} else if u.Type != game.UnitKing {
			c = '0' + byte(u.Type%10)
		}
		rows[u.Pos.Y][u.Pos.X] = c
	}
	out := "\n"
	for y, r := range rows {
		out += fmt.Sprintf("%2d %s\n", y, r)
	}
	return out
}

// TestSimEval — сравнение уровней: SIEGE_EVAL=12 go test ./internal/bot -run SimEval -v
//
// Роли закреплены за местами, поэтому уровни сравниваются по ролям: как
// держит замок бот каждого уровня против двух средних и кто из двух
// осаждающих разного уровня первым доберётся до короля. Пары осаждающих
// идут в обоих порядках: осада I ходит в дне раньше осады II.
func TestSimEval(t *testing.T) {
	n := 0
	fmt.Sscan(os.Getenv("SIEGE_EVAL"), &n)
	if n == 0 {
		t.Skip("сравнение уровней: SIEGE_EVAL=<число партий на расклад>")
	}
	type res struct{ held, tookA, tookB, days, rejects, stalls int }
	lineups := [][3]Level{
		{Easy, Normal, Normal}, {Normal, Normal, Normal}, {Hard, Normal, Normal},
		{Normal, Hard, Normal}, {Normal, Normal, Hard},
		{Normal, Hard, Easy}, {Normal, Easy, Hard},
		{Hard, Hard, Hard}, {Easy, Easy, Easy},
	}
	if only := os.Getenv("SIEGE_EVAL_ONLY"); only != "" {
		var pick [][3]Level
		for _, lu := range lineups {
			if strings.Contains(only, fmt.Sprintf("%s,%s,%s", lu[0], lu[1], lu[2])) {
				pick = append(pick, lu)
			}
		}
		lineups = pick
	}
	for _, lu := range lineups {
		out := make(chan res, n)
		for i := 0; i < n; i++ {
			go func(seed int64) {
				_, st := simulate(t, lu, seed*7)
				r := res{rejects: st.rejects, stalls: st.stalls}
				if st.result != nil {
					r.days = min(st.result.Days, game.SiegeDays)
					switch {
					case !st.result.Captured:
						r.held = 1
					case st.result.CapturedBy == game.SideAttackerA.String():
						r.tookA = 1
					default:
						r.tookB = 1
					}
				}
				out <- r
			}(int64(i + 1))
		}
		var sum res
		for i := 0; i < n; i++ {
			r := <-out
			sum.held += r.held
			sum.tookA += r.tookA
			sum.tookB += r.tookB
			sum.days += r.days
			sum.rejects += r.rejects
			sum.stalls += r.stalls
		}
		t.Logf("замок %-6s осада I %-6s осада II %-6s: замок выстоял %d из %d, короля взяли I %d / II %d; в среднем %d дней, отбито %d, зависло %d",
			lu[0], lu[1], lu[2], sum.held, n, sum.tookA, sum.tookB, sum.days/n, sum.rejects, sum.stalls)
	}
}
