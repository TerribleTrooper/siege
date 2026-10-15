package bot

import (
	"encoding/json"
	"os"
	"testing"

	"siege/internal/game"
)

// Позиции для проверок собираются из обычной партии: отряды и стена в
// состоянии открыты, а всё остальное идёт по правилам.

func newGame() *game.State { return game.New([3]string{"Аня", "Боря", "Витя"}) }

// turnTo прокручивает ходы до нужной стороны.
func turnTo(t *testing.T, s *game.State, side game.Side) {
	t.Helper()
	for i := 0; i < 6 && s.Turn != side; i++ {
		if err := s.Apply(s.Turn, game.Action{Kind: game.ActEndTurn}); err != nil {
			t.Fatal(err)
		}
	}
	if s.Turn != side {
		t.Fatalf("не дошла очередь до %s", side)
	}
}

// put ставит отряд на поле. Номера берём с запасом, чтобы не пересечься с
// теми, что раздаёт сама партия.
func put(s *game.State, side game.Side, t game.UnitType, at game.Point) *game.Unit {
	sp := game.Spec(t)
	u := &game.Unit{ID: 1000 + len(s.Units), Owner: side, Type: t, HP: sp.MaxHP, Pos: at, Charges: sp.Charges}
	s.Units = append(s.Units, u)
	return u
}

func unitAt(s *game.State, p game.Point) *game.Unit {
	for _, u := range s.Units {
		if u.Pos == p && u.Carrier == 0 {
			return u
		}
	}
	return nil
}

// next — что бот сделает следующим, глядя со своего места.
func next(b *Bot, s *game.State) game.Action {
	v := s.ViewFor(s.SeatOf(s.Turn))
	return b.Next(&v)
}

// playTurn отыгрывает ход бота до конца и возвращает его действия.
func playTurn(t *testing.T, b *Bot, s *game.State) []game.Action {
	t.Helper()
	side := s.Turn
	var out []game.Action
	for i := 0; i < 80 && s.Turn == side && s.Phase == game.PhasePlay; i++ {
		a := next(b, s)
		if err := s.Apply(side, a); err != nil {
			t.Fatalf("сервер отбил действие бота %+v: %v", a, err)
		}
		out = append(out, a)
	}
	return out
}

// Подрывник у стены не ждёт: ответного хода у замка не будет.
func TestBomberChargesUnderWall(t *testing.T) {
	s := newGame()
	turnTo(t, s, game.SideAttackerA)
	bomber := put(s, game.SideAttackerA, game.UnitBomber, game.Point{X: 10, Y: game.WallRow + 1})
	a := next(New(Normal, 1), s)
	if a.Kind != game.ActCharge || a.UnitID != bomber.ID || a.To.Y != game.WallRow {
		t.Fatalf("подрывник у целой стены должен взрывать её, а бот сделал %+v", a)
	}
}

// Отряд рядом с королём бьёт короля: ради этого вся осада.
func TestAttackerStrikesKing(t *testing.T) {
	s := newGame()
	turnTo(t, s, game.SideAttackerA)
	king := s.King(game.SideDefender)
	inf := put(s, game.SideAttackerA, game.UnitInfantry, king.Pos.Add(0, 1))
	for _, lv := range []Level{Easy, Normal, Hard} {
		a := next(New(lv, 1), s)
		if a.Kind != game.ActAttack || a.UnitID != inf.ID || a.To != king.Pos {
			t.Fatalf("%s: пехота рядом с королём должна бить его, а бот сделал %+v", lv, a)
		}
	}
}

// Подрывник в двух шагах от стены — главная цель замка.
func TestCastleKillsBomberFirst(t *testing.T) {
	s := newGame()
	turnTo(t, s, game.SideDefender)
	scout := unitAt(s, game.Point{X: game.WallFirstCol + 3, Y: game.WalkRow})
	if scout == nil || scout.Type != game.UnitScout {
		t.Fatal("на старте над стеной стоит разведчик")
	}
	// Не в кустах: в зарослях подрывник прятался бы и от стрелков.
	bomber := put(s, game.SideAttackerA, game.UnitBomber, scout.Pos.Add(1, 3))
	put(s, game.SideAttackerA, game.UnitInfantry, scout.Pos.Add(2, 3))
	playTurn(t, New(Normal, 1), s)
	for _, u := range s.Units {
		if u == bomber {
			t.Fatal("замок должен был снять подрывника у стены")
		}
	}
}

// Пролом в стене — король уходит от него сразу, а не когда враг уже рядом:
// за пару дней он должен оказаться в дальнем углу двора.
func TestKingLeavesBreach(t *testing.T) {
	s := newGame()
	turnTo(t, s, game.SideDefender)
	king := s.King(game.SideDefender)
	gap := game.Point{X: king.Pos.X, Y: game.WallRow}
	s.Wall[gap.X-game.WallFirstCol].HP = 0
	put(s, game.SideAttackerA, game.UnitInfantry, gap.Add(0, 2))
	b := New(Normal, 1)
	for day := 0; day < 3; day++ {
		playTurn(t, b, s)
		turnTo(t, s, game.SideDefender)
	}
	if d := game.Chebyshev(king.Pos, gap); d < 6 { // шаг короля — две клетки
		t.Fatalf("за три дня король должен уйти от пролома подальше, а он в %d клетках (%v)", d, king.Pos)
	}
}

// Воодушевление стоит всего обоза, и бот читает его, когда оно превращает
// два удара в убитого короля.
func TestRallyForRegicide(t *testing.T) {
	s := newGame()
	// Копим на заклинание: неистраченные очки действия идут в обоз.
	for s.Supplies(game.SideAttackerA) < game.RallyCost {
		turnTo(t, s, game.SideDefender)
		turnTo(t, s, game.SideAttackerA)
	}
	king := s.King(game.SideDefender)
	king.HP = 2*game.Spec(game.UnitInfantry).Damage + 1 // двоих мало, двоих воодушевлённых хватит
	put(s, game.SideAttackerA, game.UnitInfantry, king.Pos.Add(-1, 1))
	put(s, game.SideAttackerA, game.UnitInfantry, king.Pos.Add(1, 1))
	b := New(Normal, 1)
	if a := next(b, s); a.Kind != game.ActAbility || a.Ability != "rally" {
		t.Fatalf("бот должен воодушевить войска перед ударом по королю, а сделал %+v", a)
	}
	playTurn(t, b, s)
	if s.Phase == game.PhasePlay || s.Result == nil || !s.Result.Captured {
		t.Fatal("после воодушевления король должен пасть")
	}
}

// Новобранец тратит за ход не больше своих очков: остальное уходит в обоз.
func TestEasyBotKeepsAP(t *testing.T) {
	s := newGame()
	b := New(Easy, 3)
	for day := 0; day < 6; day++ {
		turnTo(t, s, game.SideAttackerA)
		side := s.Turn
		for s.Turn == side {
			a := next(b, s)
			if a.Kind == game.ActEndTurn && game.APPerTurn-s.AP > profiles[Easy].apBudget {
				t.Fatalf("день %d: новобранец потратил %d очков", s.Day, game.APPerTurn-s.AP)
			}
			if err := s.Apply(side, a); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Бот видит ровно то, что видел бы человек на его месте. Отряд, спрятанный
// туманом, не меняет ни снимок, ни решение бота.
func TestBotIgnoresHiddenUnits(t *testing.T) {
	s := newGame()
	turnTo(t, s, game.SideDefender)
	view := func() []byte {
		v := s.ViewFor(s.SeatOf(game.SideDefender))
		b, _ := json.Marshal(v)
		return b
	}
	before := view()
	plain := next(New(Hard, 5), s)
	// Подрывник в глубине поля: со стены его не видно.
	put(s, game.SideAttackerB, game.UnitBomber, game.Point{X: 14, Y: 16})
	if string(view()) != string(before) {
		t.Fatal("отряд в тумане не должен попадать в снимок замка")
	}
	if got := next(New(Hard, 5), s); got != plain {
		t.Fatalf("решение бота не должно зависеть от того, чего он не видит: %+v против %+v", got, plain)
	}
}

// Партии ботов всех уровней на всех ролях проходят до конца, и сервер не
// отбивает им ни одного действия: бот предлагает только то, что разрешено
// правилами.
func TestBotsPlayCleanMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("целые партии")
	}
	for i, lu := range [][3]Level{{Hard, Normal, Easy}, {Easy, Hard, Normal}, {Normal, Easy, Hard}} {
		_, st := simulate(t, lu, int64(11+i))
		if st.result == nil {
			t.Fatalf("%v: партия должна дойти до конца", lu)
		}
		if st.rejects > 0 || st.stalls > 0 {
			t.Fatalf("%v: отбито %d действий, зависало %d ходов: %v", lu, st.rejects, st.stalls, st.rejected)
		}
	}
}

// Воевода в первые дни сажает людей на пашню и на рудник — каждого на своё
// место, а не обоих на одно.
func TestAttackerTakesFarmAndMine(t *testing.T) {
	s := newGame()
	b := New(Normal, 2)
	for day := 0; day < 10; day++ {
		turnTo(t, s, game.SideAttackerA)
		playTurn(t, b, s)
	}
	farm := unitAt(s, game.FarmTiles()[0])
	if farm == nil || farm.Owner != game.SideAttackerA {
		t.Fatal("за десять дней на своей пашне должен стоять отряд")
	}
	held := 0
	for _, m := range game.MineTiles()[:2] {
		if u := unitAt(s, m); u != nil && u.Owner == game.SideAttackerA {
			held++
		}
	}
	if held == 0 {
		t.Fatal("за десять дней на руднике в поле должен стоять отряд")
	}
}

// Король прячется в дальнем углу двора, а у осады внутри только рыцари с
// обзором в одну клетку. Бот должен прочесать двор и найти его, а не
// кружить у пустого трона: так и было, пока он не помнил, что уже осмотрел.
func TestAttackersFindHiddenKing(t *testing.T) {
	s := newGame()
	king := s.King(game.SideDefender)
	// Замок остаётся с одним королём: никто не мешает искать.
	for _, u := range append([]*game.Unit{}, s.Units...) {
		if u.Owner == game.SideDefender && u != king {
			for i, x := range s.Units {
				if x == u {
					s.Units = append(s.Units[:i], s.Units[i+1:]...)
					break
				}
			}
		}
	}
	king.Pos = game.Point{X: game.WallLastCol - 1, Y: game.CourtRowFirst}
	s.Wall[game.GateColLeft-game.WallFirstCol].HP = 0
	for dx := -1; dx <= 1; dx++ {
		put(s, game.SideAttackerA, game.UnitKnight, game.Point{X: game.GateColLeft + dx, Y: game.CourtRowLast})
	}
	b := New(Hard, 4)
	for day := 0; day < 8; day++ {
		turnTo(t, s, game.SideAttackerA)
		acts := playTurn(t, b, s)
		if os.Getenv("SIEGE_FIND") != "" {
			var at []game.Point
			for _, u := range s.Units {
				if u.Owner == game.SideAttackerA {
					at = append(at, u.Pos)
				}
			}
			t.Logf("день %d: рыцари %v, круг %d %v\nходы %+v", s.Day, at, len(b.kingMaybe()), b.kingMaybe(), acts)
		}
		if s.Phase != game.PhasePlay {
			return // нашли и убили
		}
		for _, u := range s.Units {
			if u.Owner == game.SideAttackerA && game.Chebyshev(u.Pos, king.Pos) <= 1 {
				return // нашли: рыцарь стоит рядом с королём
			}
		}
	}
	t.Fatalf("за восемь дней рыцари не нашли короля в углу двора (%v)", king.Pos)
}
