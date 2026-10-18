package game

import (
	"encoding/json"
	"strings"
	"testing"
)

func newTestGame() *State { return New([3]string{"Аня", "Боря", "Витя"}) }

// nextTurn гарантированно начинает для side новый ход (с полным запасом AP).
func nextTurn(t *testing.T, s *State, side Side) {
	t.Helper()
	s.endTurn()
	giveTurn(t, s, side)
}

// giveTurn прокручивает ходы до нужной стороны (пока осада идёт).
func giveTurn(t *testing.T, s *State, side Side) {
	t.Helper()
	for i := 0; i < 12 && s.Turn != side; i++ {
		if s.Phase != PhasePlay {
			t.Fatalf("осада закончилась раньше, чем дошла очередь до %s", side)
		}
		s.endTurn()
	}
	if s.Turn != side {
		t.Fatalf("не удалось передать ход стороне %s", side)
	}
}

func TestTurnOrderAndDays(t *testing.T) {
	s := newTestGame()
	if s.Turn != SideDefender || s.Day != 1 {
		t.Fatalf("старт: ждали защитника в день 1, получили %s день %d", s.Turn, s.Day)
	}
	s.endTurn()
	if s.Turn != SideAttackerA {
		t.Fatalf("после защитника ходит A, а не %s", s.Turn)
	}
	s.endTurn()
	s.endTurn()
	if s.Day != 2 || s.Turn != SideDefender {
		t.Fatalf("после полного круга ждали день 2 и защитника, получили день %d / %s", s.Day, s.Turn)
	}
}

// Замок выстоял — партия окончена: следующей осады нет, и ходить больше
// некому.
func TestDefenderHoldsAndScores(t *testing.T) {
	s := newTestGame()
	for i := 0; i < SiegeDays*3+3 && s.Phase == PhasePlay; i++ {
		s.endTurn()
	}
	if s.Phase != PhaseOver {
		t.Fatalf("ждали конец партии, фаза %s", s.Phase)
	}
	if s.Result == nil || s.Result.Captured {
		t.Fatalf("замок не должен быть взят без атак: %+v", s.Result)
	}
	defender := s.Seats[s.SeatOf(SideDefender)]
	if defender.Score != ScoreDefenderHold {
		t.Fatalf("защитник получил %d очков вместо %d", defender.Score, ScoreDefenderHold)
	}
	if !strings.Contains(s.Result.Summary, "Побеждает "+defender.Name) {
		t.Fatalf("в итоге должен быть назван победитель: %q", s.Result.Summary)
	}
	if err := s.Apply(s.Turn, Action{Kind: ActEndTurn}); err != ErrPhase {
		t.Fatalf("после конца партии ходить нельзя, получили %v", err)
	}
}

func TestIntactWallBlocksAttacker(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 1
	u := s.spawn(SideAttackerA, UnitInfantry, Point{col, WallRow + 1})
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: u.ID, To: Point{col, WallRow}}); err == nil {
		t.Fatal("пехота прошла сквозь целую стену")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: u.ID, To: Point{col, WallRow + 2}}); err != nil {
		t.Fatalf("обычный шаг по полю должен работать: %v", err)
	}
}

func TestRamBreaksGateThenCapture(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	ram := s.spawn(SideAttackerA, UnitRam, Point{GateColRight, WallRow + 1})
	// Таран бьёт только ворота.
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: ram.ID, To: Point{WallFirstCol + 2, WallRow}}); err == nil {
		t.Fatal("таран не должен бить обычную стену")
	}
	hits := 0
	for s.segmentCol(GateColRight).HP > 0 && hits < 20 {
		if s.AP == 0 {
			giveTurn(t, s, SideDefender)
			giveTurn(t, s, SideAttackerA)
		}
		if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: ram.ID, To: Point{GateColRight, WallRow}}); err != nil {
			t.Fatalf("удар тарана: %v", err)
		}
		hits++
		s.turnCounter++ // таран успевает ударить раз в ход
	}
	if !s.segmentCol(GateColRight).Breached() {
		t.Fatal("ворота не пробиты")
	}
	if !s.breached[SideAttackerA] {
		t.Fatal("пролом не зачтён атакующему A")
	}

	// Через пролом пехота доходит до короля и добивает его: приз осады
	// теперь фигура, а не клетка.
	nextTurn(t, s, SideAttackerA)
	s.removeUnit(ram) // таран откатили, путь свободен
	king := s.King(SideDefender)
	if king == nil {
		t.Fatal("у замка должен быть король")
	}
	for _, def := range append([]*Unit{}, s.Units...) {
		if def.Owner == SideDefender && def != king {
			s.removeUnit(def) // гарнизон выбит: проверяем геометрию прохода
		}
	}
	king.Pos = Point{GateColRight, CourtRowLast} // вплотную к боевому ходу
	king.HP = 1

	inf := s.spawn(SideAttackerA, UnitInfantry, Point{GateColRight, WallRow + 1})
	inf.movedTurn, inf.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: inf.ID, To: Point{GateColRight, WalkRow}}); err != nil {
		t.Fatalf("вход в пролом: %v", err)
	}
	s.turnCounter++
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: inf.ID, To: king.Pos}); err != nil {
		t.Fatalf("удар по королю: %v", err)
	}
	if s.Phase != PhaseOver {
		t.Fatal("убийство короля должно завершать партию")
	}
	if !s.Result.Captured || s.Result.CapturedBy != SideAttackerA.String() {
		t.Fatalf("неверный итог партии: %+v", s.Result)
	}
	if got := s.Seats[s.SeatOf(SideAttackerA)].Score; got != ScoreCapture {
		t.Fatalf("захватчик получил %d вместо %d", got, ScoreCapture)
	}
}

func TestBomberBreachesWithOneCharge(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 2
	bomb := s.spawn(SideAttackerA, UnitBomber, Point{col, WallRow + 1})
	bomb.movedTurn, bomb.actedTurn = 0, 0

	targets := s.chargeTargets(bomb)
	if !containsPoint(targets, Point{col, WallRow}) {
		t.Fatalf("соседний целый сегмент должен быть целью: %v", targets)
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActCharge, UnitID: bomb.ID, To: Point{col, WallRow}}); err != nil {
		t.Fatalf("закладка заряда: %v", err)
	}
	if !s.segmentCol(col).Breached() {
		t.Fatal("одного заряда должно хватать на сегмент целиком")
	}
	if !s.breached[SideAttackerA] {
		t.Fatal("пролом не зачтён подрывнику")
	}
	if s.unitByID(bomb.ID) != nil {
		t.Fatal("подрывник должен погибнуть во взрыве")
	}
}

func TestBomberNeedsToStandUnderIntactWall(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 2

	// Издали заряд не заложить.
	far := s.spawn(SideAttackerA, UnitBomber, Point{col, WallRow + 3})
	far.movedTurn, far.actedTurn = 0, 0
	if len(s.chargeTargets(far)) != 0 {
		t.Fatal("издали подрывнику закладывать нечего")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActCharge, UnitID: far.ID, To: Point{col, WallRow}}); err == nil {
		t.Fatal("подрывник не должен доставать стену издали")
	}

	// И в готовый пролом тоже: подрывать там уже нечего.
	s.segmentCol(col).HP = 0
	near := s.spawn(SideAttackerA, UnitBomber, Point{col, WallRow + 1})
	near.movedTurn, near.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActCharge, UnitID: near.ID, To: Point{col, WallRow}}); err == nil {
		t.Fatal("в проломе закладывать нечего")
	}

	// Пехота зарядов не носит.
	inf := s.spawn(SideAttackerA, UnitInfantry, Point{WallFirstCol + 5, WallRow + 1})
	inf.movedTurn, inf.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActCharge, UnitID: inf.ID, To: Point{WallFirstCol + 5, WallRow}}); err == nil {
		t.Fatal("заряды закладывает только подрывник")
	}
}

func TestAttackersCannotFightEachOther(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	spot := openArea(t, s, 2)
	a := s.spawn(SideAttackerA, UnitInfantry, spot)
	s.spawn(SideAttackerB, UnitInfantry, spot.Add(0, 1))
	a.movedTurn, a.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: a.ID, To: spot.Add(0, 1)}); err == nil {
		t.Fatal("осаждающие не должны атаковать друг друга")
	}
}

func TestUnusedAPBecomesSupplies(t *testing.T) {
	s := newTestGame()
	before := s.Supplies(SideDefender)
	s.endTurn() // защитник не потратил ни одного очка
	// Каждое неизрасходованное очко — припас, без предела: ход, который не
	// успели потратить, целиком уходит в обоз.
	if got := s.Supplies(SideDefender); got != before+APPerTurn*APCarryPerPoint {
		t.Fatalf("неиспользованные очки не стали припасами: %d -> %d", before, got)
	}
	// Половина хода — половина прибавки.
	giveTurn(t, s, SideDefender)
	before = s.Supplies(SideDefender)
	spent := 3
	if err := s.spendAP(spent); err != nil {
		t.Fatalf("трата очков: %v", err)
	}
	s.endTurn()
	if got, want := s.Supplies(SideDefender)-before, (APPerTurn-spent)*APCarryPerPoint; got != want {
		t.Fatalf("после %d потраченных очков в припасы ушло %d, ждали %d", spent, got, want)
	}
}

func TestRebuildClosesEmptyBreach(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = RebuildCost * 3
	seg := s.segmentCol(GateColLeft)
	seg.HP = 0

	before := s.Supplies(SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: GateColLeft}); err != nil {
		t.Fatalf("отстройка: %v", err)
	}
	if seg.HP != seg.MaxHP {
		t.Fatalf("отстроенный сегмент должен быть целым: %d из %d", seg.HP, seg.MaxHP)
	}
	if got := before - s.Supplies(SideDefender); got != RebuildCost {
		t.Fatalf("отстройка списала %d припасов, ждали %d", got, RebuildCost)
	}

	// Целый сегмент не трогают: латать стену по кусочку больше нечем.
	seg.HP = seg.MaxHP / 2
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: GateColLeft}); err == nil {
		t.Fatal("отстраивать можно только пролом")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "repair", Col: GateColLeft}); err == nil {
		t.Fatal("старого ремонта стены больше нет")
	}

	// И в занятый пролом не встроишься.
	seg.HP = 0
	s.spawn(SideAttackerA, UnitInfantry, Point{GateColLeft, WallRow})
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: GateColLeft}); err == nil {
		t.Fatal("нельзя отстроить пролом, в котором стоит враг")
	}
}

// Роли закреплены за местами на всю партию: первое держит замок, второе и
// третье осаждают. Мета отдаёт ту же раскладку — по ней лобби подписывает
// места ещё до начала.
func TestSeatsKeepRoles(t *testing.T) {
	s := newTestGame()
	meta := NewMeta()
	for seat, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
		if got := s.SideOf(seat); got != side {
			t.Fatalf("место %d играет за %s, а не за %s", seat, got, side)
		}
		if got := s.SeatOf(side); got != seat {
			t.Fatalf("за %s играет место %d, а не %d", side, got, seat)
		}
		if meta.SeatSides[seat] != side.String() {
			t.Fatalf("мета подписывает место %d как %s, а не %s", seat, meta.SeatSides[seat], side)
		}
	}
	if s.SideOf(-1) != SideNeutral || s.SideOf(3) != SideNeutral {
		t.Fatal("у наблюдателя роли нет")
	}
}

func TestDeployOnlyInOwnZone(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: Point{Cols - 1, CampRowLast}}); err == nil {
		t.Fatal("нельзя разворачиваться в чужом лагере")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "garrison", To: Point{0, CampRowLast}}); err == nil {
		t.Fatal("осаждающий не может нанять гарнизон")
	}
	before := s.Supplies(SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: Point{0, CampRowLast}}); err != nil {
		t.Fatalf("найм в своём лагере: %v", err)
	}
	if got := s.Supplies(SideAttackerA); got != before-Spec(UnitInfantry).Cost {
		t.Fatalf("припасы списались неверно: %d -> %d", before, got)
	}
}

// Регрессия: стена закрывает не всю карту, и раньше замок можно было обойти
// по краю, ни разу не ударив по стене.
func TestCliffsBlockFlanking(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	u := s.spawn(SideAttackerA, UnitInfantry, Point{0, WallRow + 1})
	u.movedTurn, u.actedTurn = 0, 0

	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: u.ID, To: Point{0, WallRow}}); err == nil {
		t.Fatal("по флангу можно пройти мимо стены")
	}
	for _, p := range s.Reachable(u) {
		if p.Y <= WallRow && (p.X < WallFirstCol || p.X > WallLastCol) {
			t.Fatalf("клетка %v не должна быть достижима: это скала", p)
		}
	}
	// А вот у защитника внутренний двор должен оставаться связным.
	giveTurn(t, s, SideDefender)
	g := s.spawn(SideDefender, UnitGarrison, Point{WallFirstCol + 1, ThroneRow})
	g.movedTurn, g.actedTurn = 0, 0
	if err := s.Apply(SideDefender, Action{Kind: ActMove, UnitID: g.ID, To: Point{WallFirstCol + 3, ThroneRow}}); err != nil {
		t.Fatalf("защитник должен свободно ходить по двору: %v", err)
	}
}

// Карта обязана быть зеркальной: иначе один из осаждающих ближе к стене, и
// вся гонка за пролом теряет смысл.
//
// Из проверки исключены рудники. Двор замка достаётся одному игроку целиком,
// и клетка в его углу соперников между собой не разводит. Полевые же стоят
// по одному на каждой стороне центра — северный равноудалён от обоих лагерей
// (это проверяет TestMinesSitWhereTowersStood), южный на клетку ближе ко
// второму осаждающему. Зеркальной пары у них быть не может: на карте чётной
// ширины центральной колонки попросту нет.
func TestTerrainIsMirrorSymmetric(t *testing.T) {
	mine := map[Point]bool{}
	for _, p := range MineTiles() {
		mine[p] = true
	}
	for y := 0; y < Rows; y++ {
		for x := 0; x < Cols; x++ {
			p, mirror := Point{x, y}, Point{Cols - 1 - x, y}
			if mine[p] || mine[mirror] {
				continue
			}
			left, right := TileAt(p), TileAt(mirror)
			if left != right {
				t.Fatalf("клетки (%d,%d)=%s и (%d,%d)=%s не зеркальны", x, y, left, Cols-1-x, y, right)
			}
		}
	}
}

// Пашни по флангам: ровно по одной на каждый край и строго напротив друг
// друга, иначе один из осаждающих получает лишний доход даром.
func TestFarmsAreMirroredAndPaySupplies(t *testing.T) {
	farms := FarmTiles()
	if len(farms) != 2 {
		t.Fatalf("ждали две пашни, получили %d", len(farms))
	}
	if farms[0].Y != farms[1].Y || farms[0].X != Cols-1-farms[1].X {
		t.Fatalf("пашни не зеркальны: %v и %v", farms[0], farms[1])
	}
	for _, p := range farms {
		if !IsFarm(p) {
			t.Fatalf("клетка %v должна быть пашней, а она %s", p, TileAt(p))
		}
	}

	s := newTestGame()
	s.spawn(SideAttackerA, UnitInfantry, farms[0])
	s.spawn(SideAttackerA, UnitInfantry, farms[1])
	if s.FarmsHeldBy(SideAttackerA) != 2 {
		t.Fatal("обе пашни должны числиться за тем, кто на них стоит")
	}
	before := s.Supplies(SideAttackerA)
	// Если рядом с пашней случится наблюдательный пункт, его доход тоже
	// попадёт в ход — включаем в ожидание.
	towers := s.towersHeld(SideAttackerA)
	giveTurn(t, s, SideAttackerA)
	// В начале хода капают и обычные припасы, и надбавка с пашен.
	want := before + AttackerSupplyGet + 2*FarmSupply + towers*TowerSupply
	if got := s.Supplies(SideAttackerA); got != want {
		t.Fatalf("за две пашни ждали %d припасов, получили %d", want, got)
	}
}

func TestRavineBlocksMovement(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	ravine, from := findTile(t, s, TileRavine)
	u := s.spawn(SideAttackerA, UnitInfantry, from)
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: u.ID, To: ravine}); err == nil {
		t.Fatal("юнит спустился в обрыв")
	}
	for _, p := range s.Reachable(u) {
		if p == ravine {
			t.Fatal("обрыв попал в список доступных клеток")
		}
	}
}

func TestBushCostsExtraMovement(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	bush, from := findTile(t, s, TileBush)
	u := s.spawn(SideAttackerA, UnitScout, from) // шаг 2
	reach := map[Point]bool{}
	for _, p := range s.Reachable(u) {
		reach[p] = true
	}
	if !reach[bush] {
		t.Fatal("куст должен быть достижим: он стоит ровно весь запас хода")
	}
	if MoveCost(bush) != BushMoveCost || MoveCost(from) != 1 {
		t.Fatalf("цена входа посчитана неверно: куст %d, поле %d", MoveCost(bush), MoveCost(from))
	}
	// Второй куст в двух шагах стоит 1+2=3 — запаса в 2 не хватит.
	for y := FieldRowFirst; y < CampRowFirst; y++ {
		for x := 0; x < Cols; x++ {
			far := Point{x, y}
			if TileAt(far) == TileBush && Chebyshev(far, from) == 2 && reach[far] {
				t.Fatalf("дальний куст %v не должен быть достижим за 2 очка хода", far)
			}
		}
	}
}

func TestBushGivesCoverNotBlockade(t *testing.T) {
	// Стрелять через кусты можно — но урон срезается укрытием.
	s := newTestGame()
	bush, _ := findTile(t, s, TileBush)
	giveTurn(t, s, SideAttackerA)
	// Стрелок встаёт в двух клетках: вплотную укрытие не работает.
	shooter := s.spawn(SideAttackerA, UnitScout, bush.Add(0, 2))
	if Chebyshev(shooter.Pos, bush) != 2 {
		t.Fatalf("ждали дистанцию 2, получили %d", Chebyshev(shooter.Pos, bush))
	}
	victim := s.spawn(SideDefender, UnitGarrison, bush)
	// Проверяем именно укрытие, а не засаду: пусть за этим отрядом следили,
	// когда он входил в заросли.
	victim.revealAll()
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: shooter.ID, To: bush}); err != nil {
		t.Fatalf("по цели в кустах стрелять можно: %v", err)
	}
	expected := Spec(UnitScout).Damage - BushCoverCut
	if expected < MinRangedHit {
		expected = MinRangedHit
	}
	if got := Spec(UnitGarrison).MaxHP - victim.HP; got != expected {
		t.Fatalf("укрытие срезало урон неверно: получено %d, ждали %d", got, expected)
	}

	// И клетка в кустах должна быть в подсветке целей.
	found := false
	for _, p := range s.AttackTargets(shooter) {
		if p == bush {
			found = true
		}
	}
	if !found && shooter.actedTurn != s.turnCounter {
		t.Fatal("цель в кустах должна подсвечиваться как доступная")
	}

	// Стрельба из кустов тоже разрешена.
	s2 := newTestGame()
	bush2, _ := findTile(t, s2, TileBush)
	giveTurn(t, s2, SideAttackerA)
	bush = bush2
	inBush := s2.spawn(SideAttackerA, UnitScout, bush)
	s2.spawn(SideDefender, UnitGarrison, bush.Add(0, 2))
	if err := s2.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: inBush.ID, To: bush.Add(0, 2)}); err != nil {
		t.Fatalf("из кустов стрелять можно: %v", err)
	}
}

func TestMinePaysSilver(t *testing.T) {
	s := newTestGame()
	// Клетки рудника в поле: третья лежит во дворе замка, осаждающим до неё
	// не дойти, и к этой проверке она не относится.
	var tiles []Point
	for _, p := range MineTiles() {
		if p.Y >= FieldRowFirst {
			tiles = append(tiles, p)
		}
	}
	if len(tiles) != 2 {
		t.Fatalf("ожидали две клетки рудника в поле, получили %d", len(tiles))
	}
	for _, p := range tiles {
		s.spawn(SideAttackerA, UnitInfantry, p)
	}
	if s.Silver(SideAttackerA) != 0 {
		t.Fatal("серебро не должно начисляться до начала хода")
	}
	giveTurn(t, s, SideAttackerA)
	if got := s.Silver(SideAttackerA); got != 2*SilverPerTile {
		t.Fatalf("за две клетки рудника ждали %d серебра, получили %d", 2*SilverPerTile, got)
	}
	if s.Silver(SideDefender) != 0 {
		t.Fatal("защитник рудник не держит, а серебро получил")
	}
	if s.MineHeldBy(SideAttackerA) != 2 {
		t.Fatal("рудник числится не за тем, кто на нём стоит")
	}
}

func TestEliteCostsSilverOnly(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	camp := Point{0, CampRowLast}

	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "knight", To: camp}); err == nil {
		t.Fatal("рыцаря наняли без серебра")
	}
	s.silver[SideAttackerA] = Spec(UnitKnight).Silver
	supplies := s.Supplies(SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "knight", To: camp}); err != nil {
		t.Fatalf("наём рыцаря за серебро: %v", err)
	}
	if s.Silver(SideAttackerA) != 0 {
		t.Fatalf("серебро не списалось: %d", s.Silver(SideAttackerA))
	}
	if s.Supplies(SideAttackerA) != supplies {
		t.Fatal("элита не должна стоить припасов")
	}

	// Защитник нанимает ту же элиту — рудник нужен всем троим.
	giveTurn(t, s, SideDefender)
	s.silver[SideDefender] = Spec(UnitCrossbow).Silver
	if err := s.Apply(SideDefender, Action{Kind: ActDeploy, Unit: "crossbow", To: Point{WallFirstCol + 2, CourtRowLast}}); err != nil {
		t.Fatalf("защитник тоже может нанять арбалетчиков: %v", err)
	}
}

// findTile ищет на карте клетку нужного типа и свободную клетку рядом с ней.
// clearTowers убирает с поля наблюдательные пункты: вышка рядом с отрядом
// даёт лишний обзор, и для проверок, где важен именно туман, это источник
// плавающих падений. На старте их уже нет, но тесты их ставят сами.
func clearTowers(s *State) {
	for _, b := range append([]*Building{}, s.Buildings...) {
		if b.Kind == BuildTower {
			s.removeBuilding(b)
		}
	}
}

// addWatchPost ставит наблюдательный пункт напрямую, без очков действия и
// припасов: готовых вышек на карте больше нет, а проверять их свойства надо.
func addWatchPost(s *State, owner Side, at Point) *Building {
	hp := buildingHP(BuildTower)
	b := &Building{ID: s.nextBuildingID, Kind: BuildTower, Owner: owner, Pos: at, HP: hp, MaxHP: hp}
	s.nextBuildingID++
	s.Buildings = append(s.Buildings, b)
	return b
}

// fieldSpot — свободная клетка открытого поля подальше от стены: место под
// вышку в тестах.
func fieldSpot(t *testing.T, s *State, from Point) Point {
	t.Helper()
	for y := from.Y; y < CampRowFirst; y++ {
		for x := from.X; x < Cols; x++ {
			p := Point{x, y}
			if TileAt(p) == TileField && s.unitAt(p) == nil && s.buildingAt(p) == nil {
				return p
			}
		}
	}
	t.Fatal("на поле не нашлось свободной клетки")
	return Point{}
}

func findTile(t *testing.T, probe *State, kind TileKind) (target, neighbour Point) {
	t.Helper()
	for y := FieldRowFirst; y < CampRowFirst; y++ {
		for x := 0; x < Cols; x++ {
			p := Point{x, y}
			if TileAt(p) != kind || probe.buildingAt(p) != nil {
				continue
			}
			for _, off := range neighborOffsets {
				n := p.Add(off[0], off[1])
				if n.InBounds() && TileAt(n) == TileField && probe.buildingAt(n) == nil {
					return p, n
				}
			}
		}
	}
	t.Fatalf("на карте нет клетки типа %s с открытым соседом", kind)
	return Point{}, Point{}
}

// openArea расчищает кусок карты под тесты геометрии и возвращает его центр.
// Рельеф и постройки расставлены плотно, поэтому искать
// готовую пустую площадку ненадёжно — проще временно убрать помехи.
func openArea(t *testing.T, s *State, r int) Point {
	t.Helper()
	centre := Point{Cols/2 - 5, (FieldRowFirst + CampRowFirst) / 2}
	saved := map[Point]TileKind{}
	for dy := -r - 1; dy <= r+1; dy++ {
		for dx := -r - 1; dx <= r+1; dx++ {
			p := centre.Add(dx, dy)
			if !p.InBounds() {
				continue
			}
			switch k := Classic.TileAt(p); k {
			case TileBush, TileRavine, TileMine, TileFarm:
				saved[p] = k
				Classic.setTile(p, TileField)
			}
			if b := s.buildingAt(p); b != nil {
				s.removeBuilding(b)
			}
			if u := s.unitAt(p); u != nil {
				s.removeUnit(u)
			}
		}
	}
	t.Cleanup(func() {
		for p, k := range saved {
			Classic.setTile(p, k)
		}
	})
	return centre
}

// Юниты ходят по квадрату: диагональ стоит столько же, сколько прямой шаг,
// поэтому зона хода — квадрат, а не ромб.
func TestMovementIsSquareNotDiamond(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	move := Spec(UnitInfantry).Move
	center := openArea(t, s, move)
	u := s.spawn(SideAttackerA, UnitInfantry, center)

	reach := map[Point]bool{}
	for _, p := range s.Reachable(u) {
		reach[p] = true
	}
	corner := center.Add(move, move)
	if !reach[corner] {
		t.Fatalf("угол квадрата %v должен быть достижим: диагональ стоит один шаг", corner)
	}
	if reach[center.Add(move+1, 0)] {
		t.Fatalf("за пределы запаса хода уходить нельзя")
	}
	// Любая достижимая клетка лежит внутри квадрата со стороной 2*move.
	for p := range reach {
		if Chebyshev(center, p) > move {
			t.Fatalf("клетка %v вне квадрата хода", p)
		}
	}
}

func TestDiagonalCannotSqueezeBetweenObstacles(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	center := openArea(t, s, 2)
	u := s.spawn(SideAttackerA, UnitInfantry, center)

	// Запираем угол двумя кольями: пройти по диагонали между ними нельзя.
	for _, p := range []Point{center.Add(1, 0), center.Add(0, 1)} {
		s.Buildings = append(s.Buildings, &Building{
			ID: 900 + p.X, Kind: BuildBarricade, Owner: SideDefender, Pos: p,
			HP: BarricadeHP, MaxHP: BarricadeHP,
		})
	}
	vis := s.VisibleFor(SideAttackerA)
	if s.stepAllowed(u, center, [2]int{1, 1}, vis) {
		t.Fatal("юнит протиснулся по диагонали между двумя заграждениями")
	}
	// А если один из проходов свободен — шаг по диагонали разрешён.
	if !s.stepAllowed(u, center, [2]int{-1, -1}, vis) {
		t.Fatal("свободную диагональ закрывать нельзя")
	}
}

func TestBuildingsBlockAndFeedTheCastle(t *testing.T) {
	s := newTestGame()
	if n := s.countBuildings(SideDefender, BuildBarracks); n != 2 {
		t.Fatalf("в замке должно быть двое казарм, нашлось %d", n)
	}
	barracks := s.Buildings[0]
	if barracks.Kind != BuildBarracks {
		t.Fatalf("ожидали казармы первыми в списке, получили %s", barracks.Kind)
	}
	// Постройка занимает клетку.
	if s.passable(SideDefender, barracks.Pos) {
		t.Fatal("через казармы нельзя ходить")
	}

	income := func() int {
		before := s.Supplies(SideDefender)
		for i := 0; i < 3; i++ {
			s.endTurn()
		}
		// В доход попадают и неиспользованные очки действия — все до одного.
		return s.Supplies(SideDefender) - before - APPerTurn*APCarryPerPoint
	}
	full := income()
	if full != DefenderSupplyGet+2*BarracksSupply {
		t.Fatalf("доход с двумя казармами %d, ждали %d", full, DefenderSupplyGet+2*BarracksSupply)
	}
	s.removeBuilding(barracks)
	if got := income(); got != full-BarracksSupply {
		t.Fatalf("после потери казарм доход %d, ждали %d", got, full-BarracksSupply)
	}
}

func TestTowerExtendsDefenderRange(t *testing.T) {
	s := newTestGame()
	tower := addWatchPost(s, SideDefender, openArea(t, s, 2))
	near := s.spawn(SideDefender, UnitScout, tower.Pos.Add(0, 1))
	far := s.spawn(SideDefender, UnitScout, Point{WallFirstCol, WalkRow})
	base := Spec(UnitScout).Range
	if got := s.unitRange(near); got != base+TowerRangeBonus {
		t.Fatalf("у башни дальность %d, ждали %d", got, base+TowerRangeBonus)
	}
	if got := s.unitRange(far); got != base {
		t.Fatalf("вдали от башни дальность %d, ждали %d", got, base)
	}
	s.removeBuilding(tower)
	if got := s.unitRange(near); got != base {
		t.Fatalf("разрушенная башня продолжает давать бонус: %d", got)
	}
}

func TestWorkshopDiscountsEngines(t *testing.T) {
	s := newTestGame()
	full := Spec(UnitCatapult).Cost
	if got := s.unitCost(SideAttackerA, UnitCatapult); got != full-WorkshopCut {
		t.Fatalf("с мастерской катапульта стоит %d, ждали %d", got, full-WorkshopCut)
	}
	if got := s.unitCost(SideAttackerA, UnitInfantry); got != Spec(UnitInfantry).Cost {
		t.Fatal("скидка мастерской не должна касаться пехоты")
	}
	for _, b := range append([]*Building{}, s.Buildings...) {
		if b.Kind == BuildWorkshop && b.Owner == SideAttackerA {
			s.removeBuilding(b)
		}
	}
	if got := s.unitCost(SideAttackerA, UnitCatapult); got != full {
		t.Fatalf("без мастерской катапульта должна стоить %d, получили %d", full, got)
	}
}

func TestEngineerTrapIsHiddenAndHurts(t *testing.T) {
	s := newTestGame()
	spot := openArea(t, s, 2)
	eng := s.spawn(SideDefender, UnitEngineer, spot)
	trapAt := spot.Add(1, 0)

	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "trap", To: trapAt}); err != nil {
		t.Fatalf("установка ловушки: %v", err)
	}
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "trap", To: spot.Add(0, 1)}); err == nil {
		t.Fatal("инженеры не должны строить дважды за ход")
	}

	// Защитник свою ловушку видит, осаждающий — нет.
	if len(s.ViewFor(s.SeatOf(SideDefender)).Traps) != 1 {
		t.Fatal("защитник должен видеть свою ловушку")
	}
	if len(s.ViewFor(s.SeatOf(SideAttackerA)).Traps) != 0 {
		t.Fatal("осаждающий не должен видеть ловушку до срабатывания")
	}

	giveTurn(t, s, SideAttackerA)
	victim := s.spawn(SideAttackerA, UnitInfantry, trapAt.Add(1, 0))
	victim.movedTurn, victim.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: victim.ID, To: trapAt}); err != nil {
		t.Fatalf("шаг на ловушку: %v", err)
	}
	if victim.HP != Spec(UnitInfantry).MaxHP-TrapDamage {
		t.Fatalf("ловушка нанесла %d урона, ждали %d", Spec(UnitInfantry).MaxHP-victim.HP, TrapDamage)
	}
	if s.trapAt(trapAt) != nil {
		t.Fatal("сработавшая ловушка должна исчезать")
	}
}

func TestEngineerBarricadeAndRepair(t *testing.T) {
	s := newTestGame()
	spot := openArea(t, s, 2)
	eng := s.spawn(SideDefender, UnitEngineer, spot)
	s.supplies[SideDefender] = BarricadeCost // на старте обоза на прясло не хватает
	at := spot.Add(1, 0)
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "barricade", To: at}); err != nil {
		t.Fatalf("установка частокола: %v", err)
	}
	if s.passable(SideAttackerA, at) {
		t.Fatal("частокол должен перекрывать клетку")
	}

	// Инженеры чинят постройки, а не стену: кладку либо отстраивают целиком,
	// либо она лежит.
	s2 := newTestGame()
	seg := s2.segmentCol(GateColLeft)
	seg.HP = 10
	fixer := s2.spawn(SideDefender, UnitEngineer, Point{GateColLeft - 1, WalkRow})
	if err := s2.Apply(SideDefender, Action{Kind: ActBuild, UnitID: fixer.ID, Build: "repair", To: Point{GateColLeft, WallRow}}); err == nil {
		t.Fatal("инженеры не должны латать стену")
	}
	if seg.HP != 10 {
		t.Fatalf("стена всё-таки залатана: %d", seg.HP)
	}

	var barracks *Building
	for _, b := range s2.Buildings {
		if b.Kind == BuildBarracks && b.Owner == SideDefender {
			barracks = b
		}
	}
	if barracks == nil {
		t.Fatal("у замка должны быть казармы")
	}
	barracks.HP = 10
	mender := s2.spawn(SideDefender, UnitEngineer, barracks.Pos.Add(1, 0))
	if err := s2.Apply(SideDefender, Action{Kind: ActBuild, UnitID: mender.ID, Build: "repair", To: barracks.Pos}); err != nil {
		t.Fatalf("починка постройки инженером: %v", err)
	}
	if barracks.HP != 10+EngineerRepair {
		t.Fatalf("после починки %d, ждали %d", barracks.HP, 10+EngineerRepair)
	}
}

// Туман есть у всех, но осаждающие смотрят общими глазами: что разведал
// один, видит и второй.
func TestFogIsSharedBetweenAttackers(t *testing.T) {
	s := newTestGame()
	far := openArea(t, s, 2)
	lurker := s.spawn(SideDefender, UnitGarrison, far)

	seenBy := func(side Side) bool {
		for _, u := range s.ViewFor(s.SeatOf(side)).Units {
			if u.ID == lurker.ID {
				return true
			}
		}
		return false
	}
	if seenBy(SideAttackerA) || seenBy(SideAttackerB) {
		t.Fatal("чужой отряд посреди поля виден без единого разведчика")
	}

	// Разведчика выставляет только A — но видят оба. Пехота на такое уже не
	// годится: она видит ровно соседнюю клетку.
	scout := s.spawn(SideAttackerA, UnitScout, far.Add(2, 0))
	if !seenBy(SideAttackerA) {
		t.Fatalf("отряд в %d клетках от разведчика должен быть виден", Chebyshev(scout.Pos, far))
	}
	if !seenBy(SideAttackerB) {
		t.Fatal("осаждающие делят обзор: разведка союзника работает на обоих")
	}
	// А замку туман остался при нём: в лагерь осаждающих он не заглядывает.
	if s.CanSee(SideDefender, Point{0, CampRowLast}) {
		t.Fatal("замок не должен просматривать лагерь осаждающих")
	}
	// И обзор с союзником он не делит: чужие глаза работают только на осаду.
	if s.CanSee(SideDefender, scout.Pos) != s.VisibleFor(SideDefender)[scout.Pos] {
		t.Fatal("CanSee и VisibleFor разошлись")
	}
}

func TestFogAlwaysShowsOwnBase(t *testing.T) {
	s := newTestGame()
	defVis := s.VisibleFor(SideDefender)
	for x := WallFirstCol; x <= WallLastCol; x++ {
		for y := 0; y <= WallRow; y++ {
			if !defVis[Point{x, y}] {
				t.Fatalf("защитник не видит собственный замок в (%d,%d)", x, y)
			}
		}
	}
	// Осаждающему видны оба лагеря — свой и союзника, — но не двор замка.
	atkVis := s.VisibleFor(SideAttackerA)
	if !atkVis[Point{0, CampRowLast}] {
		t.Fatal("осаждающий не видит собственный лагерь")
	}
	if !atkVis[Point{Cols - 1, CampRowLast}] {
		t.Fatal("лагерь союзника по осаде тоже должен быть виден")
	}
	if atkVis[Point{GateColLeft, ThroneRow}] {
		t.Fatal("двор замка не должен просматриваться из лагеря")
	}
}

// Готовых вышек на карте нет вовсе: обзор на поле — вложение, а не лотерея
// расстановки.
func TestNoWatchPostsOnFreshMap(t *testing.T) {
	s := newTestGame()
	for _, b := range s.Buildings {
		if b.Kind == BuildTower {
			t.Fatalf("на старте не должно быть вышек, а в %v стоит", b.Pos)
		}
	}
}

// Вышку ставят обе стороны, но разными руками и за разные деньги: у замка
// инженеры за 25, у осаждающих пехота за 30.
func TestWatchPostBuilders(t *testing.T) {
	if WatchPostCost(SideDefender) != 25 || WatchPostCost(SideAttackerA) != 30 {
		t.Fatalf("цены вышек: замок %d, осада %d", WatchPostCost(SideDefender), WatchPostCost(SideAttackerA))
	}

	// Замок: инженеры умеют, гарнизон — нет.
	s := newTestGame()
	s.supplies[SideDefender] = 500
	spot := openArea(t, s, 2)
	eng := s.spawn(SideDefender, UnitEngineer, spot)
	eng.movedTurn, eng.actedTurn = 0, 0
	at := spot.Add(1, 0)
	before := s.Supplies(SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "watchpost", To: at}); err != nil {
		t.Fatalf("инженеры ставят вышку: %v", err)
	}
	b := s.buildingAt(at)
	if b == nil || b.Kind != BuildTower || b.Owner != SideDefender {
		t.Fatalf("вышка не поставлена: %+v", b)
	}
	if got := before - s.Supplies(SideDefender); got != WatchPostCostDef {
		t.Fatalf("замок заплатил %d, ждали %d", got, WatchPostCostDef)
	}
	gar := s.spawn(SideDefender, UnitGarrison, spot.Add(0, 1))
	gar.movedTurn, gar.actedTurn = 0, 0
	if s.canWatchPost(gar) {
		t.Fatal("гарнизон вышек не строит")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: gar.ID, Build: "watchpost", To: spot.Add(-1, 1)}); err == nil {
		t.Fatal("гарнизон не должен ставить вышку")
	}

	// Осада: пехота умеет, разведчик — нет.
	s2 := newTestGame()
	giveTurn(t, s2, SideAttackerA)
	s2.supplies[SideAttackerA] = 500
	spot2 := openArea(t, s2, 2)
	foot := s2.spawn(SideAttackerA, UnitInfantry, spot2)
	foot.movedTurn, foot.actedTurn = 0, 0
	before2 := s2.Supplies(SideAttackerA)
	if err := s2.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: foot.ID, Build: "watchpost", To: spot2.Add(1, 0)}); err != nil {
		t.Fatalf("пехота ставит вышку: %v", err)
	}
	if got := before2 - s2.Supplies(SideAttackerA); got != WatchPostCostAtk {
		t.Fatalf("осада заплатила %d, ждали %d", got, WatchPostCostAtk)
	}
	sc := s2.spawn(SideAttackerA, UnitScout, spot2.Add(0, 1))
	sc.movedTurn, sc.actedTurn = 0, 0
	if s2.canWatchPost(sc) {
		t.Fatal("разведчик вышек не строит")
	}
}

// Больше трёх вышек на сторону не держат: иначе поле зарастает ими.
func TestWatchPostLimit(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = 1000
	for i := 0; i < WatchPostMax; i++ {
		addWatchPost(s, SideDefender, fieldSpot(t, s, Point{1, FieldRowFirst + i*2}))
	}
	spot := openArea(t, s, 2)
	eng := s.spawn(SideDefender, UnitEngineer, spot)
	eng.movedTurn, eng.actedTurn = 0, 0
	if s.canWatchPost(eng) {
		t.Fatalf("на пределе в %d вышек строить нельзя", WatchPostMax)
	}
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "watchpost", To: spot.Add(1, 0)}); err == nil {
		t.Fatalf("четвёртая вышка должна быть запрещена")
	}
}

// Пустая вышка остаётся за тем, кто её построил, но её можно отнять, заняв.
func TestWatchPostStaysWithItsBuilder(t *testing.T) {
	s := newTestGame()
	clearTowers(s)
	at := openArea(t, s, 2)
	b := addWatchPost(s, SideAttackerA, at)
	if s.towerOwner(b) != SideAttackerA {
		t.Fatal("пустая вышка должна оставаться за построившим")
	}
	s.spawn(SideDefender, UnitGarrison, at)
	if s.towerOwner(b) != SideDefender {
		t.Fatal("занявший вышку забирает её себе")
	}
}

// Рудники стоят на месте бывших центральных вышек: один ближе к замку,
// другой — к лагерям, и между ними шесть рядов.
func TestMinesSitWhereTowersStood(t *testing.T) {
	field := []Point{}
	for _, p := range MineTiles() {
		if p.Y >= FieldRowFirst {
			field = append(field, p)
		}
	}
	if len(field) != 2 {
		t.Fatalf("рудников в поле %d, ждали два", len(field))
	}
	north, south := field[0], field[1]
	if north.Y >= MineRow || south.Y <= MineRow {
		t.Fatalf("рудники должны быть по разные стороны центра: %v и %v", north, south)
	}
	if got := south.Y - north.Y; got != MineGap*2 {
		t.Fatalf("между рудниками %d рядов, ждали %d", got, MineGap*2)
	}
	for _, p := range field {
		if !IsMine(p) {
			t.Fatalf("клетка %v должна быть рудником, а она %s", p, TileAt(p))
		}
	}
	// Центр поля освободился: старых рудников там больше нет.
	for _, p := range []Point{{MineColLeft, MineRow}, {MineColRight, MineRow}} {
		if IsMine(p) {
			t.Fatalf("в центре %v рудника быть не должно", p)
		}
	}
	// Северный равноудалён от обоих лагерей.
	nearA, nearB := Point{CampWidth - 1, CampRowFirst}, Point{Cols - CampWidth, CampRowFirst}
	if Chebyshev(north, nearA) != Chebyshev(north, nearB) {
		t.Fatalf("северный рудник неравноудалён: до A %d, до B %d",
			Chebyshev(north, nearA), Chebyshev(north, nearB))
	}
}

func TestTowerServesWhoeverHoldsIt(t *testing.T) {
	s := newTestGame()
	tower := addWatchPost(s, SideNeutral, openArea(t, s, 2))
	spot := tower.Pos.Add(1, 0)
	if !s.passable(SideAttackerA, spot) {
		spot = tower.Pos.Add(0, 1)
	}
	archer := s.spawn(SideAttackerA, UnitScout, spot)

	if got := s.unitRange(archer); got != Spec(UnitScout).Range+TowerRangeBonus {
		t.Fatalf("у башни дальность %d, ждали %d", got, Spec(UnitScout).Range+TowerRangeBonus)
	}
	if !s.towerHeldBy(tower, SideAttackerA) {
		t.Fatal("башню должен держать тот, чей отряд стоит рядом")
	}
	if s.towerHeldBy(tower, SideDefender) {
		t.Fatal("башню держит только тот, кто рядом")
	}
	// Обзор башни работает на её держателя.
	vis := s.VisibleFor(SideAttackerA)
	probe := tower.Pos.Add(VisionTower-1, 0)
	if probe.InBounds() && !vis[probe] {
		t.Fatalf("башня должна просматривать %v", probe)
	}
}

func TestCatapultStrikesAreaEvenBlind(t *testing.T) {
	s := newTestGame()
	clearTowers(s) // иначе случайная башня у катапульты вскроет цель обзором
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 3)
	cat := s.spawn(SideAttackerA, UnitCatapult, centre.Add(0, Spec(UnitCatapult).Range-1))
	cat.movedTurn, cat.actedTurn = 0, 0

	bullseye := s.spawn(SideDefender, UnitGarrison, centre)
	edge := s.spawn(SideDefender, UnitGarrison, centre.Add(1, 1))
	outside := s.spawn(SideDefender, UnitGarrison, centre.Add(2, 2))

	// Весь смысл навесного оружия: бить по клетке, которой не видишь.
	// Своих наблюдателей рядом нет, значит цель и правда в тумане.
	if s.CanSee(SideAttackerA, centre) {
		t.Fatalf("клетка %v не должна просматриваться: проверяем именно слепой огонь", centre)
	}
	for _, u := range []*Unit{bullseye, edge} {
		if s.VisibleUnit(u, SideAttackerA) {
			t.Fatal("цели под накрытием не должны быть видны осаждающему")
		}
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: centre}); err != nil {
		t.Fatalf("навесной удар: %v", err)
	}

	full := Spec(UnitCatapult).Damage
	half := maxInt(full/SplashEdgeDiv, MinRangedHit)
	if got := Spec(UnitGarrison).MaxHP - bullseye.HP; got != full {
		t.Fatalf("в центре урон %d, ждали %d", got, full)
	}
	if got := Spec(UnitGarrison).MaxHP - edge.HP; got != half {
		t.Fatalf("по краю накрытия урон %d, ждали %d", got, half)
	}
	if outside.HP != Spec(UnitGarrison).MaxHP {
		t.Fatal("за пределами квадрата 3×3 задевать никого не должно")
	}
}

// Камни не разбирают, кто под ними: свой отряд рядом с целью тоже получит.
// Отдельно от слепого огня — с союзником рядом клетка уже просматривается.
func TestCatapultHitsOwnTroopsToo(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 3)
	cat := s.spawn(SideAttackerA, UnitCatapult, centre.Add(0, Spec(UnitCatapult).Range-1))
	cat.movedTurn, cat.actedTurn = 0, 0
	friend := s.spawn(SideAttackerA, UnitInfantry, centre.Add(-1, 0))
	rival := s.spawn(SideAttackerB, UnitInfantry, centre.Add(0, -1))

	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: centre}); err != nil {
		t.Fatalf("навесной удар: %v", err)
	}
	if friend.HP == Spec(UnitInfantry).MaxHP {
		t.Fatal("камни не разбирают своих: отряд рядом тоже должен получить урон")
	}
	if rival.HP != Spec(UnitInfantry).MaxHP {
		t.Fatal("отряды второго осаждающего правилами вне войны и под накрытие не попадают")
	}
}

func TestScoutingBumpCostsTurnButRevealsNothingExtra(t *testing.T) {
	// Находка стоит в кустах вплотную: идти некуда, и отряд остаётся на
	// месте. Про находку дальше по пути — в
	// TestReconByFireStopsNextToTheBlocker: там марш обрывается не на
	// старте, а на последней свободной клетке перед ней.
	s := newTestGame()
	bush, _, open := bushPair(t)
	ambush := s.spawn(SideDefender, UnitGarrison, bush)
	ambush.spotted = [4]bool{}
	ambush.spotted[SideDefender] = true

	walker := s.spawn(SideAttackerA, UnitInfantry, open)
	giveTurn(t, s, SideAttackerA)
	walker.movedTurn, walker.actedTurn = 0, 0

	if s.VisibleUnit(ambush, SideAttackerA) {
		t.Fatal("засада не должна быть видна до столкновения")
	}
	reachable := false
	for _, p := range s.Reachable(walker) {
		if p == bush {
			reachable = true
		}
	}
	if !reachable {
		t.Fatal("клетка с невидимым противником должна выглядеть свободной")
	}

	ap := s.AP
	from := walker.Pos
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: walker.ID, To: bush}); err != nil {
		t.Fatalf("шаг в засаду: %v", err)
	}
	if walker.Pos != from {
		t.Fatal("наткнувшись на чужой отряд, юнит не должен занимать его клетку")
	}
	if s.AP != ap-1 {
		t.Fatalf("очко действия должно потратиться: было %d, стало %d", ap, s.AP)
	}
	if ambush.HP != Spec(UnitGarrison).MaxHP {
		t.Fatal("столкновение — это не атака")
	}
	if !s.VisibleUnit(ambush, SideAttackerA) {
		t.Fatal("зато теперь засада вскрыта")
	}
}

// Хроники больше нет, и единственным укрытием осталcя туман: нанятый в
// глубине лагеря отряд не должен попадать в чужой снимок вообще никак.
func TestHiredUnitStaysOutOfEnemySnapshot(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "bomber", To: Point{0, CampRowLast}}); err != nil {
		t.Fatalf("наём подрывника: %v", err)
	}

	for _, u := range s.ViewFor(s.SeatOf(SideDefender)).Units {
		if u.Type == "bomber" {
			t.Fatal("защитник увидел подрывника в чужом лагере")
		}
	}
	found := false
	for _, u := range s.ViewFor(s.SeatOf(SideAttackerA)).Units {
		if u.Type == "bomber" {
			found = true
		}
	}
	if !found {
		t.Fatal("сам наниматель своего подрывника видеть обязан")
	}
}

// Регрессия: когда туман скрывал все отряды, пустой срез уезжал в JSON как
// null, и клиент падал на первом же обращении к списку.
func TestViewNeverSendsNullLists(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerB) // у этой стороны на поле пока никого
	raw, err := json.Marshal(s.ViewFor(s.SeatOf(SideAttackerB)))
	if err != nil {
		t.Fatalf("сериализация снимка: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("разбор снимка: %v", err)
	}
	for _, field := range []string{"units", "buildings", "mine", "seats", "wall", "moat"} {
		value, ok := probe[field]
		if !ok {
			t.Fatalf("в снимке нет поля %q", field)
		}
		if string(value) == "null" {
			t.Fatalf("поле %q ушло как null — клиент на таком спотыкается", field)
		}
	}
}

// Катапульта обязана уметь ходить: до стены от лагеря её надо довезти.
func TestCatapultMovesAndStrikesSameTurn(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 3)
	cat := s.spawn(SideAttackerA, UnitCatapult, centre)

	moves := s.Reachable(cat)
	if len(moves) == 0 {
		t.Fatal("у катапульты нет ни одной клетки для шага")
	}
	// Снимок тоже должен отдавать клетки шага: без них клиент её не сдвинет.
	var inView []Point
	for _, uv := range s.ViewFor(s.SeatOf(SideAttackerA)).Units {
		if uv.ID == cat.ID {
			inView = uv.Moves
		}
	}
	if len(inView) == 0 {
		t.Fatal("в снимке у катапульты пустой список moves")
	}

	to := centre.Add(0, -1)
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: cat.ID, To: to}); err != nil {
		t.Fatalf("шаг катапульты: %v", err)
	}
	if cat.Pos != to {
		t.Fatalf("катапульта осталась на %v вместо %v", cat.Pos, to)
	}
	// Подъехал — и в тот же день стреляет: ход и выстрел считаются отдельно.
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: to.Add(0, -3)}); err != nil {
		t.Fatalf("выстрел после шага: %v", err)
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: cat.ID, To: centre}); err == nil {
		t.Fatal("за один день катапульта не должна ходить дважды")
	}
}

func TestCatapultsAreCappedPerAttacker(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 100

	max := Spec(UnitCatapult).MaxAlive
	if max <= 0 {
		t.Fatal("у катапульты должен быть предел по количеству")
	}
	spots := []Point{{0, CampRowFirst}, {1, CampRowFirst}, {2, CampRowFirst}, {4, CampRowFirst}}
	var last *Unit
	for i := 0; i < max; i++ {
		if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "catapult", To: spots[i]}); err != nil {
			t.Fatalf("наём катапульты №%d: %v", i+1, err)
		}
		last = s.unitAt(spots[i])
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "catapult", To: spots[max]}); err == nil {
		t.Fatalf("осаждающий не должен держать больше %d катапульт", max)
	}
	// Второй осаждающий считается отдельно — предел на каждого, а не на всех.
	giveTurn(t, s, SideAttackerB)
	s.supplies[SideAttackerB] = 100
	if err := s.Apply(SideAttackerB, Action{Kind: ActDeploy, Unit: "catapult", To: Point{Cols - 1, CampRowFirst}}); err != nil {
		t.Fatalf("у второго осаждающего свой запас: %v", err)
	}
	// Потерял машину — освободилось место.
	giveTurn(t, s, SideAttackerA)
	s.removeUnit(last)
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "catapult", To: spots[max]}); err != nil {
		t.Fatalf("после потери катапульты наём снова открыт: %v", err)
	}
	// И в лавке видно, что место занято.
	for _, b := range s.ViewFor(s.SeatOf(SideAttackerA)).Buyable {
		if b.Unit == "catapult" && (b.Max != max || b.Have != max || b.Affordable || b.Why != WhyMax) {
			t.Fatalf("лавка показывает катапульту как доступную: have=%d max=%d affordable=%v why=%q",
				b.Have, b.Max, b.Affordable, b.Why)
		}
	}
}

// Отстройка пролома должна стоить дорого: иначе «сидеть и чинить» выигрывает
// само собой. Ориентир — недельный доход замка.
func TestRebuildCostsAWeekOfIncome(t *testing.T) {
	week := DefenderSupplyGet * 7
	if RebuildCost < week-DefenderSupplyGet {
		t.Fatalf("отстройка (%d) должна стоить около недели дохода (%d)", RebuildCost, week)
	}
	if Spec(UnitCatapult).SiegeDamage <= 0 {
		t.Fatal("катапульта обязана ломать стену")
	}
}

func TestOutpostSpawnsUnitsAndExpires(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 3)
	foot := s.spawn(SideAttackerA, UnitInfantry, centre)
	s.supplies[SideAttackerA] = OutpostCost + 10

	at := centre.Add(1, 0)
	apBefore := s.AP
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: foot.ID, Build: "outpost", To: at}); err != nil {
		t.Fatalf("установка аванпоста: %v", err)
	}
	if s.AP != apBefore-OutpostAPCost {
		t.Fatalf("аванпост стоит %d ОД: было %d, стало %d", OutpostAPCost, apBefore, s.AP)
	}
	if got := s.Supplies(SideAttackerA); got != 10 {
		t.Fatalf("после аванпоста осталось %d припасов, ждали 10", got)
	}
	b := s.buildingAt(at)
	if b == nil || b.Kind != BuildOutpost || b.Owner != SideAttackerA {
		t.Fatalf("аванпост не появился на %v", at)
	}

	// Главное: отряды выходят прямо у аванпоста, а не в лагере у края карты.
	spawnAt := at.Add(1, 0)
	if TileAt(spawnAt) != TileField {
		t.Fatalf("для проверки нужна клетка открытого поля, а %v — %s", spawnAt, TileAt(spawnAt))
	}
	if !s.deployZone(SideAttackerA, spawnAt) {
		t.Fatalf("клетка %v у аванпоста должна быть зоной найма", spawnAt)
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: spawnAt}); err != nil {
		t.Fatalf("наём у аванпоста: %v", err)
	}
	// Второй аванпост не поставить: иначе ими засыпали бы всё поле.
	other := s.spawn(SideAttackerA, UnitInfantry, centre.Add(0, 2))
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: other.ID, Build: "outpost", To: centre.Add(1, 2)}); err == nil {
		t.Fatalf("больше %d аванпостов одновременно быть не должно", OutpostMax)
	}

	// Временный: через OutpostLife дней сворачивается сам, и наём у него гаснет.
	for i := 0; i < OutpostLife*len(turnOrder) && s.Phase == PhasePlay; i++ {
		s.endTurn()
	}
	if s.buildingAt(at) != nil {
		t.Fatalf("аванпост должен сворачиваться через %d дней (сейчас день %d)", OutpostLife, s.Day)
	}
	if s.deployZone(SideAttackerA, spawnAt) {
		t.Fatal("после сворачивания аванпоста наём у него должен прекратиться")
	}
}

// Аванпост не должен открывать наём там, где его быть не может: в чужом
// лагере, на руднике и внутри замка.
func TestOutpostDeployStaysOnOpenField(t *testing.T) {
	s := newTestGame()
	for _, p := range []Point{{Cols - 1, CampRowFirst}, MineTiles()[0], {GateColLeft, WalkRow}, {GateColLeft, WallRow}} {
		if s.outpostDeployable(p) {
			t.Fatalf("из аванпоста нельзя нанимать на %v (%s)", p, TileAt(p))
		}
	}
}

func TestOutpostPlacementLimits(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 100

	// Вплотную к стене лагерь не разбить: это тыловая постройка.
	near := Point{WallFirstCol + 6, WallRow + OutpostWallGap}
	foot := s.spawn(SideAttackerA, UnitInfantry, near)
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: foot.ID, Build: "outpost", To: near.Add(0, -1)}); err == nil {
		t.Fatalf("аванпост нельзя ставить ближе %d рядов к стене", OutpostWallGap)
	}

	// Машины лагерь не ставят.
	centre := openArea(t, s, 2)
	cat := s.spawn(SideAttackerA, UnitCatapult, centre)
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: cat.ID, Build: "outpost", To: centre.Add(1, 0)}); err == nil {
		t.Fatal("осадная машина не должна разбивать аванпост")
	}

	// И защитник тоже: аванпост — средство осады.
	giveTurn(t, s, SideDefender)
	s.supplies[SideDefender] = 100
	eng := s.spawn(SideDefender, UnitEngineer, centre.Add(0, 2))
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "outpost", To: centre.Add(1, 2)}); err == nil {
		t.Fatal("защитник не ставит аванпосты")
	}
}

// Таран — осадная машина, а не боец: по людям он не работает вовсе,
// зато ворота разносит заметно быстрее прежнего.
func TestRamHitsOnlyGates(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	ram := s.spawn(SideAttackerA, UnitRam, Point{GateColLeft, WallRow + 1})
	victim := s.spawn(SideDefender, UnitGarrison, Point{GateColLeft + 1, WallRow + 1})

	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: ram.ID, To: victim.Pos}); err == nil {
		t.Fatal("таран не должен бить по отрядам")
	}
	if victim.HP != Spec(UnitGarrison).MaxHP {
		t.Fatal("отряд рядом с тараном не должен получать урон")
	}
	// И по постройкам тоже: бревном на подвесе бьют только в ворота.
	for _, p := range s.AttackTargets(ram) {
		if TileAt(p) != TileWall {
			t.Fatalf("у тарана в целях не стена: %v (%s)", p, TileAt(p))
		}
		if seg := s.segment(p); seg == nil || seg.Kind != SegGate {
			t.Fatalf("таран целится не в ворота: столбец %d", p.X)
		}
	}

	gate := s.segmentCol(GateColLeft)
	before := gate.HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: ram.ID, To: Point{GateColLeft, WallRow}}); err != nil {
		t.Fatalf("удар по воротам: %v", err)
	}
	hit := before - gate.HP
	if want := Spec(UnitRam).Damage * Spec(UnitRam).VsWall; hit != want {
		t.Fatalf("таран снёс %d прочности, ждали %d", hit, want)
	}
	// Залатать выбитые ворота замку нечем: только отстроить заново, целиком
	// и за недельный доход.
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "repair", Col: GateColLeft}); err == nil {
		t.Fatal("ремонта стены по кусочку больше нет")
	}
}

func TestWatchTowerIsGarrisonedAndContested(t *testing.T) {
	s := newTestGame()
	tower := addWatchPost(s, SideNeutral, openArea(t, s, 2))

	// В башню можно войти — это единственная постройка, которая пускает.
	if !s.passable(SideAttackerA, tower.Pos) {
		t.Fatal("наблюдательный пункт должен пускать отряд внутрь")
	}
	archer := s.spawn(SideAttackerA, UnitScout, tower.Pos)
	if got, want := s.unitRange(archer), Spec(UnitScout).Range+TowerGarrisonBonus; got != want {
		t.Fatalf("дальность из башни %d, ждали %d", got, want)
	}
	if s.towerOwner(tower) != SideAttackerA {
		t.Fatal("занявший башню её и контролирует")
	}
	if s.towersHeld(SideAttackerA) != 1 {
		t.Fatal("занятая башня должна считаться удержанной")
	}

	// Пока в башне сидят, бьют по сидящему, а не по стенам башни.
	giveTurn(t, s, SideDefender)
	shooter := s.spawn(SideDefender, UnitCrossbow, tower.Pos.Add(0, -3))
	// Башни стоят случайно, и стрелок может оказаться в кустах. Здесь
	// проверяется укрытие башни, а не засада, поэтому стрелка раскрываем:
	// иначе удар иногда уходил бы с утроенным уроном.
	shooter.revealAll()
	hpBefore, towerHP := archer.HP, tower.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: shooter.ID, To: tower.Pos}); err != nil {
		t.Fatalf("выстрел по башне: %v", err)
	}
	if tower.HP != towerHP {
		t.Fatal("удар должен прийтись по гарнизону башни, а не по самой башне")
	}
	// Камень башни держит лучше веток: урон срезан.
	full := Spec(UnitCrossbow).Damage
	if got := hpBefore - archer.HP; got != full-TowerCoverCut {
		t.Fatalf("урон по сидящему в башне %d, ждали %d", got, full-TowerCoverCut)
	}

	// Спорная башня не достаётся никому.
	empty := s.spawn(SideAttackerB, UnitInfantry, tower.Pos.Add(1, 0))
	s.removeUnit(archer)
	s.spawn(SideAttackerA, UnitInfantry, tower.Pos.Add(-1, 0))
	if s.towerOwner(tower) != SideNeutral {
		t.Fatal("башня, у которой топчутся двое, должна оставаться ничьей")
	}
	s.removeUnit(empty)
	if s.towerOwner(tower) != SideAttackerA {
		t.Fatal("когда соперник ушёл, башня достаётся оставшемуся")
	}
}

func TestTowersPaySupplies(t *testing.T) {
	s := newTestGame()
	tower := addWatchPost(s, SideNeutral, openArea(t, s, 2))
	s.spawn(SideAttackerA, UnitInfantry, tower.Pos)

	giveTurn(t, s, SideDefender) // доход начисляют на входе в ход, следующий — A
	before := s.Supplies(SideAttackerA)
	s.endTurn()
	if s.Turn != SideAttackerA {
		t.Fatalf("ждали ход осаждающего A, идёт %s", s.Turn)
	}
	got := s.Supplies(SideAttackerA) - before - AttackerSupplyGet
	if got != TowerSupply {
		t.Fatalf("башня принесла %d припасов, ждали %d", got, TowerSupply)
	}
}

func TestCastleBuildsInCourtyard(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = 100
	spots := s.courtSpots()
	if len(spots) == 0 {
		t.Fatal("во дворе должно быть где строить")
	}

	// Кузница удешевляет отстройку пролома.
	baseCost := s.rebuildCost(SideDefender)
	apBefore := s.AP
	if err := s.Apply(SideDefender, Action{Kind: ActConstruct, Build: "forge", To: spots[0]}); err != nil {
		t.Fatalf("стройка кузницы: %v", err)
	}
	if s.AP != apBefore-ConstructAPCost {
		t.Fatalf("стройка стоит %d ОД: было %d, стало %d", ConstructAPCost, apBefore, s.AP)
	}
	if got := s.Supplies(SideDefender); got != 100-ForgeCost {
		t.Fatalf("после стройки осталось %d припасов, ждали %d", got, 100-ForgeCost)
	}
	cost := s.rebuildCost(SideDefender)
	if cost != baseCost-ForgeRebuildCut {
		t.Fatalf("кузница не удешевила отстройку: было %d, стало %d", baseCost, cost)
	}
	seg := s.segmentCol(GateColLeft)
	seg.HP = 0
	s.supplies[SideDefender] = cost
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: GateColLeft}); err != nil {
		t.Fatalf("отстройка при кузнице: %v", err)
	}
	if seg.HP != seg.MaxHP {
		t.Fatalf("сегмент не отстроен: %d из %d", seg.HP, seg.MaxHP)
	}
	s.supplies[SideDefender] = 100

	// Стрелковая башня поднимает дальность своим стрелкам поблизости.
	towerAt := spots[len(spots)-1]
	if err := s.Apply(SideDefender, Action{Kind: ActConstruct, Build: "arrowTower", To: towerAt}); err != nil {
		t.Fatalf("стройка стрелковой башни: %v", err)
	}
	near := s.spawn(SideDefender, UnitScout, Point{towerAt.X, WalkRow})
	if Chebyshev(near.Pos, towerAt) > ArrowTowerRange {
		t.Skip("двор и боевой ход оказались дальше радиуса башни")
	}
	if got, want := s.unitRange(near), Spec(UnitScout).Range+TowerRangeBonus; got != want {
		t.Fatalf("дальность у башни %d, ждали %d", got, want)
	}

	// Осаждающим во дворе строить нечего, и клетка должна быть свободной.
	giveTurn(t, s, SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActConstruct, Build: "barracks", To: spots[1]}); err == nil {
		t.Fatal("осаждающий не строит во дворе замка")
	}
	giveTurn(t, s, SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActConstruct, Build: "barracks", To: towerAt}); err == nil {
		t.Fatal("на занятую клетку строить нельзя")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActConstruct, Build: "catapult", To: spots[1]}); err == nil {
		t.Fatal("во дворе строят только из списка построек")
	}
}

// Двор большой, и без пределов замок просто застроил бы его казармами.
// Плюс вокруг короля должен оставаться воздух.
func TestCourtyardBuildLimits(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = 500

	// Вокруг короля не строят: иначе приз можно было бы замуровать.
	king := s.King(SideDefender)
	if king == nil {
		t.Fatal("у замка должен быть король")
	}
	for _, off := range neighborOffsets {
		p := king.Pos.Add(off[0], off[1])
		if TileAt(p) == TileCourtyard && s.courtBuildable(p) {
			t.Fatalf("клетка %v вплотную к королю не должна быть под стройку", p)
		}
	}

	// Казарм не больше MaxBarracks, считая две стартовые.
	built := s.countBuildings(SideDefender, BuildBarracks)
	for _, p := range s.courtSpots() {
		if s.countBuildings(SideDefender, BuildBarracks) >= MaxBarracks {
			break
		}
		s.AP = APPerTurn
		if err := s.Apply(SideDefender, Action{Kind: ActConstruct, Build: "barracks", To: p}); err != nil {
			t.Fatalf("стройка казарм: %v", err)
		}
		built++
	}
	if built != MaxBarracks {
		t.Fatalf("построено казарм %d, ждали предел %d", built, MaxBarracks)
	}
	s.AP = APPerTurn
	if err := s.Apply(SideDefender, Action{Kind: ActConstruct, Build: "barracks", To: s.courtSpots()[0]}); err == nil {
		t.Fatalf("казарм не должно быть больше %d", MaxBarracks)
	}
	// И в снимке лавка двора гаснет.
	for _, o := range s.ViewFor(s.SeatOf(SideDefender)).You.Builds {
		if o.Key == "barracks" && (o.Affordable || o.Why != WhyMax) {
			t.Fatalf("казармы на пределе должны быть недоступны в снимке, причина %q", o.Why)
		}
	}
}

// Воодушевление: вся армия бьёт вдвое сильнее, но только до конца хода и за
// всё накопленное.
func TestRallyDoublesDamageForOneTurn(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 3)

	// Без припасов заклинание не читается.
	s.supplies[SideAttackerA] = RallyCost - 1
	if err := s.Apply(SideAttackerA, Action{Kind: ActAbility, Ability: "rally"}); err == nil {
		t.Fatalf("без %d припасов воодушевления быть не должно", RallyCost)
	}

	s.supplies[SideAttackerA] = RallyCost + 10
	apBefore := s.AP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAbility, Ability: "rally"}); err != nil {
		t.Fatalf("воодушевление: %v", err)
	}
	if got := s.Supplies(SideAttackerA); got != 10 {
		t.Fatalf("после воодушевления осталось %d припасов, ждали 10", got)
	}
	if s.AP != apBefore-RallyAP {
		t.Fatalf("воодушевление стоит %d ОД: было %d, стало %d", RallyAP, apBefore, s.AP)
	}
	if !s.Rallied(SideAttackerA) {
		t.Fatal("воодушевление должно действовать сразу")
	}
	// Дважды за ход не читается, и чужие отряды оно не трогает.
	s.supplies[SideAttackerA] = RallyCost
	if err := s.Apply(SideAttackerA, Action{Kind: ActAbility, Ability: "rally"}); err == nil {
		t.Fatal("воодушевление не должно читаться дважды за ход")
	}
	if s.Rallied(SideAttackerB) || s.Rallied(SideDefender) {
		t.Fatal("воодушевление работает только на свою сторону")
	}

	// Урон по отряду удвоен.
	foot := s.spawn(SideAttackerA, UnitInfantry, centre)
	victim := s.spawn(SideDefender, UnitGarrison, centre.Add(1, 0))
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: foot.ID, To: victim.Pos}); err != nil {
		t.Fatalf("удар: %v", err)
	}
	if got, want := Spec(UnitGarrison).MaxHP-victim.HP, Spec(UnitInfantry).Damage*RallyFactor; got != want {
		t.Fatalf("воодушевлённая пехота нанесла %d, ждали %d", got, want)
	}

	// И по укреплениям тоже: таран под воодушевлением сносит ворота вдвое быстрее.
	ram := s.spawn(SideAttackerA, UnitRam, Point{GateColLeft, WallRow + 1})
	gate := s.segmentCol(GateColLeft)
	before := gate.HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: ram.ID, To: Point{GateColLeft, WallRow}}); err != nil {
		t.Fatalf("удар тарана: %v", err)
	}
	if got, want := before-gate.HP, Spec(UnitRam).Damage*Spec(UnitRam).VsWall*RallyFactor; got != want {
		t.Fatalf("воодушевлённый таран снёс %d, ждали %d", got, want)
	}

	// Со сменой хода эффект гаснет сам.
	nextTurn(t, s, SideAttackerA)
	if s.Rallied(SideAttackerA) {
		t.Fatal("воодушевление должно гаснуть со сменой хода")
	}
	foot.movedTurn, foot.actedTurn = 0, 0
	victim.HP = Spec(UnitGarrison).MaxHP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: foot.ID, To: victim.Pos}); err != nil {
		t.Fatalf("удар на следующий день: %v", err)
	}
	if got, want := Spec(UnitGarrison).MaxHP-victim.HP, Spec(UnitInfantry).Damage; got != want {
		t.Fatalf("на следующий день урон %d, ждали обычные %d", got, want)
	}
}

// Накрытие по площади тоже усиливается — это главный смысл заклинания
// для осаждающего с катапультами.
func TestRallyBoostsAreaStrike(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 3)
	cat := s.spawn(SideAttackerA, UnitCatapult, centre.Add(0, Spec(UnitCatapult).Range-1))
	bullseye := s.spawn(SideDefender, UnitGarrison, centre)

	s.supplies[SideAttackerA] = RallyCost
	if err := s.Apply(SideAttackerA, Action{Kind: ActAbility, Ability: "rally"}); err != nil {
		t.Fatalf("воодушевление: %v", err)
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: centre}); err != nil {
		t.Fatalf("навесной удар: %v", err)
	}
	if got, want := Spec(UnitGarrison).MaxHP-bullseye.HP, Spec(UnitCatapult).Damage*RallyFactor; got != want {
		t.Fatalf("воодушевлённая катапульта нанесла %d в центре, ждали %d", got, want)
	}
}

// Замку заклинание доступно так же, как осаждающим.
func TestRallyWorksForCastleToo(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = RallyCost
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rally"}); err != nil {
		t.Fatalf("воодушевление замка: %v", err)
	}
	if !s.Rallied(SideDefender) {
		t.Fatal("замок тоже должен уметь воодушевлять войска")
	}
	// В снимке это видно, и повторно его уже не предлагают.
	you := s.ViewFor(s.SeatOf(SideDefender)).You
	if !you.Rally || you.RallyOK {
		t.Fatalf("снимок: действует=%v, доступно=%v — ждали true/false", you.Rally, you.RallyOK)
	}
}

// bushPair возвращает две соседние клетки кустов и открытую клетку рядом.
func bushPair(t *testing.T) (bushA, bushB, open Point) {
	t.Helper()
	bushA, bushB, open = Point{11, 11}, Point{12, 11}, Point{11, 12}
	for _, p := range []Point{bushA, bushB} {
		if !InBush(p) {
			t.Fatalf("клетка %v должна быть кустами, а она %s", p, TileAt(p))
		}
	}
	if InBush(open) {
		t.Fatalf("клетка %v должна быть открытой, а она %s", open, TileAt(open))
	}
	return bushA, bushB, open
}

// Кусты прячут только того, за кем не следили: войти в заросли на глазах у
// противника — не значит исчезнуть.
func TestBushHidesOnlyWhenEnteredUnseen(t *testing.T) {
	s := newTestGame()
	bush, _, open := bushPair(t)
	eye := Point{11, 9} // отсюда осаждающий видит и открытую клетку, и куст

	// Смотрит разведчик: пехота теперь видит на две клетки и такой слежки
	// попросту не обеспечит.
	watcher := s.spawn(SideAttackerA, UnitScout, eye)
	sneak := s.spawn(SideDefender, UnitGarrison, open)
	if !s.VisibleUnit(sneak, SideAttackerA) {
		t.Fatal("на открытом месте под наблюдением отряд должен быть виден")
	}

	// Шаг в кусты на глазах — заросли не спасают.
	if err := s.Apply(SideDefender, Action{Kind: ActMove, UnitID: sneak.ID, To: bush}); err != nil {
		t.Fatalf("шаг в кусты: %v", err)
	}
	if !s.VisibleUnit(sneak, SideAttackerA) {
		t.Fatal("за кем следили, тот в кустах не спрячется")
	}
	if s.Lurking(sneak) {
		t.Fatal("раскрытый отряд не в засаде")
	}

	// Тот же шаг без свидетелей — и отряда на чужой карте нет.
	s.removeUnit(watcher)
	nextTurn(t, s, SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActMove, UnitID: sneak.ID, To: open}); err != nil {
		t.Fatalf("выход из кустов: %v", err)
	}
	nextTurn(t, s, SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActMove, UnitID: sneak.ID, To: bush}); err != nil {
		t.Fatalf("шаг в кусты без свидетелей: %v", err)
	}
	if !s.Lurking(sneak) {
		t.Fatal("вошедший в кусты незамеченным должен быть в засаде")
	}
	// Даже если теперь подвести наблюдателя вплотную по открытому полю.
	s.spawn(SideAttackerA, UnitInfantry, eye)
	if s.VisibleUnit(sneak, SideAttackerA) {
		t.Fatal("затаившийся в кустах не должен появляться от одного лишь обзора")
	}
	if s.VisibleUnit(sneak, SideAttackerB) {
		t.Fatal("осаждающие делят разведку — союзник тоже не должен его видеть")
	}
	// И в снимок осаждающего он не попадает.
	for _, uv := range s.ViewFor(s.SeatOf(SideAttackerA)).Units {
		if uv.ID == sneak.ID {
			t.Fatal("затаившийся отряд не должен уезжать в чужой снимок")
		}
	}
	// Целью его тоже не выбрать.
	shooter := s.spawn(SideAttackerA, UnitScout, bush.Add(0, -2))
	for _, p := range s.AttackTargets(shooter) {
		if p == bush {
			t.Fatal("затаившийся отряд не должен попадать в список целей")
		}
	}
	giveTurn(t, s, SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: shooter.ID, To: bush}); err == nil {
		t.Fatal("по затаившемуся отряду нельзя бить прицельно")
	}
}

// Удар из засады бьёт втрое — и выдаёт того, кто его нанёс.
func TestAmbushTriplesDamageThenReveals(t *testing.T) {
	s := newTestGame()
	bush, _, _ := bushPair(t)
	lurker := s.spawn(SideDefender, UnitGarrison, bush)
	lurker.spotted = [4]bool{} // вошёл в заросли незамеченным
	lurker.spotted[SideDefender] = true

	_, _, open := bushPair(t)
	victim := s.spawn(SideAttackerA, UnitRam, open)
	if Chebyshev(lurker.Pos, victim.Pos) != 1 {
		t.Fatalf("для рукопашной нужна соседняя клетка: %v и %v", lurker.Pos, victim.Pos)
	}
	if !s.concealed(lurker, SideAttackerA) {
		t.Fatal("отряд в кустах должен быть скрыт от осаждающего")
	}

	giveTurn(t, s, SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: lurker.ID, To: victim.Pos}); err != nil {
		t.Fatalf("удар из засады: %v", err)
	}
	want := Spec(UnitGarrison).Damage * AmbushFactor
	if got := Spec(UnitRam).MaxHP - victim.HP; got != want {
		t.Fatalf("удар из засады нанёс %d, ждали %d", got, want)
	}
	if !s.VisibleUnit(lurker, SideAttackerA) {
		t.Fatal("ударивший из зарослей выдаёт себя")
	}

	// Второй удар с той же позиции — уже обычный.
	nextTurn(t, s, SideDefender)
	hp := victim.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: lurker.ID, To: victim.Pos}); err != nil {
		t.Fatalf("второй удар: %v", err)
	}
	if got := hp - victim.HP; got != Spec(UnitGarrison).Damage {
		t.Fatalf("второй удар нанёс %d, ждали обычные %d", got, Spec(UnitGarrison).Damage)
	}
}

// Засаду вскрывает всё, что до неё дотянулось: случайное попадание, вход в
// тот же куст и вход в соседние заросли.
func TestAmbushIsBrokenByDamageAndNeighbours(t *testing.T) {
	bush, next, open := bushPair(t)

	hide := func(s *State, side Side, at Point) *Unit {
		u := s.spawn(side, UnitGarrison, at)
		u.spotted = [4]bool{}
		u.spotted[side] = true
		return u
	}

	// 1. Накрытие вслепую: камень нашёл того, кого не видели.
	s := newTestGame()
	lurker := hide(s, SideDefender, bush)
	cat := s.spawn(SideAttackerA, UnitCatapult, bush.Add(0, Spec(UnitCatapult).Range-1))
	// Глаза на клетке есть, но засаду они не вскрывают: кусты прячут и от них.
	s.spawn(SideAttackerA, UnitScout, open)
	if s.VisibleUnit(lurker, SideAttackerA) {
		t.Fatal("одного обзора мало, чтобы вскрыть засаду")
	}
	giveTurn(t, s, SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: bush}); err != nil {
		t.Fatalf("навесной удар по зарослям: %v", err)
	}
	if lurker.HP == Spec(UnitGarrison).MaxHP {
		t.Fatal("камень должен был задеть затаившийся отряд")
	}
	if !s.VisibleUnit(lurker, SideAttackerA) {
		t.Fatal("получивший урон отряд перестаёт быть невидимым")
	}

	// 2. Шаг на ту же клетку: разведка боем вскрывает засаду.
	s = newTestGame()
	lurker = hide(s, SideDefender, bush)
	walker := s.spawn(SideAttackerA, UnitInfantry, open)
	giveTurn(t, s, SideAttackerA)
	reachable := false
	for _, p := range s.Reachable(walker) {
		if p == bush {
			reachable = true
		}
	}
	if !reachable {
		t.Fatal("клетка с затаившимся отрядом должна выглядеть свободной")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: walker.ID, To: bush}); err != nil {
		t.Fatalf("шаг в заросли: %v", err)
	}
	if walker.Pos == bush {
		t.Fatal("занятую клетку шагом не отобрать")
	}
	if !s.VisibleUnit(lurker, SideAttackerA) {
		t.Fatal("наткнувшись на засаду, её вскрывают")
	}

	// 3. Шаг в соседний куст: в одних зарослях не разминуться.
	s = newTestGame()
	lurker = hide(s, SideDefender, bush)
	walker = s.spawn(SideAttackerA, UnitInfantry, open)
	giveTurn(t, s, SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: walker.ID, To: next}); err != nil {
		t.Fatalf("шаг в соседние кусты: %v", err)
	}
	if Chebyshev(next, bush) != 1 {
		t.Fatalf("клетки %v и %v должны соприкасаться", next, bush)
	}
	if !s.VisibleUnit(lurker, SideAttackerA) {
		t.Fatal("вошедший в соседние заросли должен обнаружить засаду")
	}
	if !s.VisibleUnit(walker, SideDefender) {
		t.Fatal("и сам обнаружиться: находка взаимная")
	}
}

// Предел армии: у осаждающих он один, у замка выше — он один против двоих.
func TestArmySizeIsCapped(t *testing.T) {
	if ArmyCap(SideAttackerA) != MaxArmyAttacker ||
		ArmyCap(SideAttackerB) != MaxArmyAttacker ||
		ArmyCap(SideDefender) != MaxArmyDefender {
		t.Fatal("пределы армий разъехались с константами")
	}
	if ArmyCap(SideDefender) <= ArmyCap(SideAttackerA) {
		t.Fatal("у замка предел должен быть выше, чем у осаждающего")
	}

	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 1000

	// Набиваем армию до предела: лагерь для двадцати отрядов мал, поэтому
	// расставляем их по полю напрямую, минуя наём.
	fill := func(side Side, upTo int) {
		for y := FieldRowFirst; y < CampRowFirst && s.ArmySize(side) < upTo; y++ {
			for x := 0; x < Cols && s.ArmySize(side) < upTo; x++ {
				p := Point{x, y}
				if TileAt(p) != TileField || s.unitAt(p) != nil || s.buildingAt(p) != nil {
					continue
				}
				s.spawn(side, UnitInfantry, p)
			}
		}
	}
	fill(SideAttackerA, MaxArmyAttacker-1)
	if s.ArmySize(SideAttackerA) != MaxArmyAttacker-1 {
		t.Fatalf("не удалось расставить %d отрядов, вышло %d", MaxArmyAttacker-1, s.ArmySize(SideAttackerA))
	}

	// Последнее место занимаем честным наймом...
	spot := s.deployTiles(SideAttackerA)[0]
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: spot}); err != nil {
		t.Fatalf("наём до предела: %v", err)
	}
	// ...а дальше предел должен держать.
	s.AP = APPerTurn
	spot = s.deployTiles(SideAttackerA)[0]
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: spot}); err == nil {
		t.Fatalf("больше %d отрядов держать нельзя", MaxArmyAttacker)
	}

	// Предел виден в снимке, и лавка гаснет целиком.
	view := s.ViewFor(s.SeatOf(SideAttackerA))
	if view.You.Army != MaxArmyAttacker || view.You.ArmyMax != MaxArmyAttacker {
		t.Fatalf("снимок показывает %d/%d, ждали %d/%d",
			view.You.Army, view.You.ArmyMax, MaxArmyAttacker, MaxArmyAttacker)
	}
	for _, b := range view.Buyable {
		if b.Affordable || b.Why != WhyArmy {
			t.Fatalf("на пределе армии ничего нанять нельзя, а %s: affordable=%v why=%q", b.Unit, b.Affordable, b.Why)
		}
	}

	// Потеряли отряд — место снова есть.
	s.removeUnit(s.Units[len(s.Units)-1])
	s.AP = APPerTurn
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: spot}); err != nil {
		t.Fatalf("после потери отряда наём снова открыт: %v", err)
	}

	// Предел считается по сторонам отдельно: сосед упёрся — вас не касается.
	giveTurn(t, s, SideAttackerB)
	s.supplies[SideAttackerB] = 100
	if err := s.Apply(SideAttackerB, Action{Kind: ActDeploy, Unit: "infantry", To: Point{Cols - 1, CampRowFirst}}); err != nil {
		t.Fatalf("у второго осаждающего свой предел: %v", err)
	}

	// А замок со своими тридцатью упирается позже осаждающего.
	s2 := newTestGame()
	s2.supplies[SideDefender] = 1000
	for _, side := range []Side{SideDefender} {
		for y := FieldRowFirst; y < CampRowFirst && s2.ArmySize(side) < MaxArmyDefender; y++ {
			for x := 0; x < Cols && s2.ArmySize(side) < MaxArmyDefender; x++ {
				if p := (Point{x, y}); TileAt(p) == TileField && s2.unitAt(p) == nil && s2.buildingAt(p) == nil {
					s2.spawn(side, UnitGarrison, p)
				}
			}
		}
	}
	if s2.ArmySize(SideDefender) != MaxArmyDefender {
		t.Fatalf("у замка на поле %d отрядов, ждали %d", s2.ArmySize(SideDefender), MaxArmyDefender)
	}
	if err := s2.Apply(SideDefender, Action{Kind: ActDeploy, Unit: "scout", To: Point{GateColLeft, WalkRow}}); err == nil {
		t.Fatalf("замок тоже не должен переходить свои %d", MaxArmyDefender)
	}
}

// Лекарь: подходит и ставит на ноги соседа. Не воюет, машины не чинит,
// одно лечение за ход.
func TestHealerPatchesNeighbours(t *testing.T) {
	s := newTestGame()
	spot := openArea(t, s, 2)
	giveTurn(t, s, SideDefender)

	doc := s.spawn(SideDefender, UnitHealer, spot)
	hurt := s.spawn(SideDefender, UnitGarrison, spot.Add(1, 0))
	hurt.HP = 2

	// Нанять лекаря может и замок, и осаждающий.
	if !canBuy(SideDefender, UnitHealer) || !canBuy(SideAttackerA, UnitHealer) {
		t.Fatal("лекарь должен быть доступен обеим сторонам")
	}
	// Сам он не боец.
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: doc.ID, To: hurt.Pos}); err == nil {
		t.Fatal("лекарь не атакует")
	}

	// В снимке подсвечен тот, кого есть смысл лечить.
	spots := s.healTargets(doc)
	if len(spots) != 1 || spots[0] != hurt.Pos {
		t.Fatalf("лекарь должен видеть одну цель %v, видит %v", hurt.Pos, spots)
	}

	ap := s.AP
	if err := s.Apply(SideDefender, Action{Kind: ActHeal, UnitID: doc.ID, To: hurt.Pos}); err != nil {
		t.Fatalf("лечение: %v", err)
	}
	if hurt.HP != 2+HealAmount {
		t.Fatalf("после лечения %d HP, ждали %d", hurt.HP, 2+HealAmount)
	}
	if s.AP != ap-1 {
		t.Fatalf("лечение стоит очко действия: было %d, стало %d", ap, s.AP)
	}
	// Второй раз за ход — нет.
	if err := s.Apply(SideDefender, Action{Kind: ActHeal, UnitID: doc.ID, To: hurt.Pos}); err == nil {
		t.Fatal("лекарь лечит один раз за ход")
	}

	// Выше предела здоровья не поднимает.
	nextTurn(t, s, SideDefender)
	hurt.HP = Spec(UnitGarrison).MaxHP - 1
	if err := s.Apply(SideDefender, Action{Kind: ActHeal, UnitID: doc.ID, To: hurt.Pos}); err != nil {
		t.Fatalf("лечение до предела: %v", err)
	}
	if hurt.HP != Spec(UnitGarrison).MaxHP {
		t.Fatalf("лечение перелило через край: %d из %d", hurt.HP, Spec(UnitGarrison).MaxHP)
	}
	// Целого лечить нечего, и в подсветку он не попадает.
	nextTurn(t, s, SideDefender)
	if len(s.healTargets(doc)) != 0 {
		t.Fatal("целый отряд не должен попадать в цели лечения")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActHeal, UnitID: doc.ID, To: hurt.Pos}); err == nil {
		t.Fatal("целого лечить нечего")
	}
}

func TestHealerLimits(t *testing.T) {
	s := newTestGame()
	spot := openArea(t, s, 3)
	giveTurn(t, s, SideAttackerA)

	doc := s.spawn(SideAttackerA, UnitHealer, spot)
	engine := s.spawn(SideAttackerA, UnitCatapult, spot.Add(1, 0))
	engine.HP = 1
	enemy := s.spawn(SideDefender, UnitGarrison, spot.Add(0, 1))
	enemy.HP = 1
	far := s.spawn(SideAttackerA, UnitInfantry, spot.Add(3, 0))
	far.HP = 1

	for _, c := range []struct {
		name string
		to   Point
	}{
		{"осадную машину", engine.Pos},
		{"чужой отряд", enemy.Pos},
		{"отряд через две клетки", far.Pos},
		{"пустую клетку", spot.Add(-1, 0)},
	} {
		if err := s.Apply(SideAttackerA, Action{Kind: ActHeal, UnitID: doc.ID, To: c.to}); err == nil {
			t.Fatalf("лекарь не должен лечить %s", c.name)
		}
	}
	if len(s.healTargets(doc)) != 0 {
		t.Fatalf("в целях лечения не должно быть никого, есть %v", s.healTargets(doc))
	}
	// И сам себя он не перевязывает.
	doc.HP = 1
	if err := s.Apply(SideAttackerA, Action{Kind: ActHeal, UnitID: doc.ID, To: doc.Pos}); err == nil {
		t.Fatal("лекарь не лечит сам себя")
	}
}

// Таран ограничен так же, как катапульта: трое разносили ворота за день.
func TestRamsAreCappedPerAttacker(t *testing.T) {
	if Spec(UnitRam).MaxAlive != 2 {
		t.Fatalf("у тарана предел %d, ждали 2", Spec(UnitRam).MaxAlive)
	}
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 100

	spots := []Point{{0, CampRowFirst}, {1, CampRowFirst}, {2, CampRowFirst}}
	for i := 0; i < Spec(UnitRam).MaxAlive; i++ {
		if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "ram", To: spots[i]}); err != nil {
			t.Fatalf("наём тарана №%d: %v", i+1, err)
		}
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "ram", To: spots[2]}); err == nil {
		t.Fatalf("таранов не должно быть больше %d", Spec(UnitRam).MaxAlive)
	}
	// Два тарана ворота за один день не сносят — в этом и смысл предела.
	gate := s.segmentCol(GateColLeft)
	perTurn := Spec(UnitRam).Damage * Spec(UnitRam).VsWall * Spec(UnitRam).MaxAlive
	if perTurn >= gate.MaxHP {
		t.Fatalf("два тарана наносят %d по воротам на %d — предел не держит", perTurn, gate.MaxHP)
	}
}

// Аванпост подорожал до вложения уровня решающего штурма.
func TestOutpostPriceIsSteep(t *testing.T) {
	if OutpostCost != 60 {
		t.Fatalf("цена аванпоста %d, ждали 60", OutpostCost)
	}
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 2)
	foot := s.spawn(SideAttackerA, UnitInfantry, centre)

	s.supplies[SideAttackerA] = OutpostCost - 1
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: foot.ID, Build: "outpost", To: centre.Add(1, 0)}); err == nil {
		t.Fatalf("без %d припасов аванпост не поставить", OutpostCost)
	}
	s.supplies[SideAttackerA] = OutpostCost
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: foot.ID, Build: "outpost", To: centre.Add(1, 0)}); err != nil {
		t.Fatalf("аванпост ровно по цене: %v", err)
	}
	if got := s.Supplies(SideAttackerA); got != 0 {
		t.Fatalf("после аванпоста осталось %d припасов, ждали 0", got)
	}
}

// В башню заходят люди, но не осадные машины: катапульта, таран и баллиста
// слишком громоздки.
func TestSiegeEnginesCannotEnterTowers(t *testing.T) {
	s := newTestGame()
	tower := addWatchPost(s, SideNeutral, openArea(t, s, 3)).Pos

	reaches := func(u *Unit) bool {
		for _, p := range s.Reachable(u) {
			if p == tower {
				return true
			}
		}
		return false
	}

	// Пехота в башню заходит...
	foot := s.spawn(SideAttackerA, UnitInfantry, tower.Add(1, 0))
	giveTurn(t, s, SideAttackerA)
	if !reaches(foot) {
		t.Fatal("пехота должна входить в башню")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: foot.ID, To: tower}); err != nil {
		t.Fatalf("вход пехоты в башню: %v", err)
	}
	s.removeUnit(foot)

	// ...а машины — нет, причём ни шагом, ни в списке доступных клеток.
	for _, tp := range []UnitType{UnitCatapult, UnitRam, UnitBallista} {
		if !Spec(tp).NoTower {
			t.Fatalf("%s должна быть помечена как не входящая в башню", Spec(tp).Label)
		}
		m := s.spawn(SideAttackerA, tp, tower.Add(1, 0))
		m.movedTurn = 0
		if reaches(m) {
			t.Fatalf("%s не должна входить в башню", Spec(tp).Label)
		}
		if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: m.ID, To: tower}); err == nil {
			t.Fatalf("%s вошла в башню шагом", Spec(tp).Label)
		}
		s.removeUnit(m)
	}

	// Лестница по этому правилу в башню заходит: её в списке исключений нет.
	ladder := s.spawn(SideAttackerA, UnitLadder, tower.Add(1, 0))
	ladder.movedTurn = 0
	if Spec(UnitLadder).NoTower {
		t.Fatal("лестница в списке исключений не значится")
	}
	if !reaches(ladder) {
		t.Fatal("лестница должна входить в башню")
	}
}

// Пустая башня — одновременно и цель, и клетка для шага. Сервер обязан
// разрешать оба действия: что именно делать, решает игрок.
func TestEmptyTowerIsBothTargetAndStep(t *testing.T) {
	s := newTestGame()
	tower := addWatchPost(s, SideNeutral, openArea(t, s, 2)).Pos
	giveTurn(t, s, SideAttackerA)
	foot := s.spawn(SideAttackerA, UnitInfantry, tower.Add(1, 0))

	inMoves, inTargets := false, false
	for _, p := range s.Reachable(foot) {
		if p == tower {
			inMoves = true
		}
	}
	for _, p := range s.AttackTargets(foot) {
		if p == tower {
			inTargets = true
		}
	}
	if !inMoves || !inTargets {
		t.Fatalf("пустая башня должна быть и шагом, и целью: шаг=%v цель=%v", inMoves, inTargets)
	}

	// Занятую башню шагом не отобрать — значит двусмысленности уже нет.
	s.spawn(SideDefender, UnitGarrison, tower)
	for _, p := range s.Reachable(foot) {
		if p == tower {
			t.Fatal("в занятую башню шагать нельзя")
		}
	}
}

// --- регрессии и новые правила ---

// Регрессия: отряд, стоящий на линии стены, был неуязвим. Удар по его клетке
// разбирался как удар по кладке — а по пролому не разбирался вовсе, и клетка
// просто не попадала в список целей.
func TestUnitInBreachIsNotInvulnerable(t *testing.T) {
	s := newTestGame()
	col := GateColLeft
	s.segmentCol(col).HP = 0 // ворота выбиты
	at := Point{col, WallRow}

	giveTurn(t, s, SideAttackerA)
	holder := s.spawn(SideAttackerA, UnitInfantry, at)

	// Защитник бьёт в пролом из боевого хода.
	giveTurn(t, s, SideDefender)
	garrison := s.spawn(SideDefender, UnitGarrison, Point{col, WalkRow})
	garrison.movedTurn, garrison.actedTurn = 0, 0

	targets := s.AttackTargets(garrison)
	if !containsPoint(targets, at) {
		t.Fatalf("пролом с чужим отрядом должен быть целью, цели: %v", targets)
	}
	hp := holder.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: garrison.ID, To: at}); err != nil {
		t.Fatalf("удар по отряду в проломе: %v", err)
	}
	if holder.HP >= hp {
		t.Fatalf("отряд в проломе не получил урона: %d -> %d", hp, holder.HP)
	}
	// И сам пролом от этого не «починился»: бить было по человеку.
	if s.segmentCol(col).HP != 0 {
		t.Fatal("удар по отряду не должен трогать сегмент")
	}
}

// То же для целых ворот: защитник может встать в своих воротах, и раньше
// осаждающий бил по кладке вокруг него, а не по нему.
func TestUnitInGateIsAttackable(t *testing.T) {
	s := newTestGame()
	at := Point{GateColRight, WallRow}

	giveTurn(t, s, SideDefender)
	sortie := s.spawn(SideDefender, UnitGarrison, at)

	giveTurn(t, s, SideAttackerA)
	inf := s.spawn(SideAttackerA, UnitInfantry, at.Add(0, 1))
	inf.movedTurn, inf.actedTurn = 0, 0

	hp, segHP := sortie.HP, s.segmentCol(GateColRight).HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: inf.ID, To: at}); err != nil {
		t.Fatalf("удар по отряду в воротах: %v", err)
	}
	if sortie.HP >= hp {
		t.Fatalf("отряд в воротах не получил урона: %d -> %d", hp, sortie.HP)
	}
	if s.segmentCol(GateColRight).HP != segHP {
		t.Fatal("удар ушёл в ворота вместо отряда")
	}
}

// Накрытие тоже должно доставать того, кто стоит в проломе: раньше камень
// уходил в кладку под ним.
func TestSplashHitsUnitOnWallLine(t *testing.T) {
	s := newTestGame()
	col := WallFirstCol + 6
	s.segmentCol(col).HP = 0
	at := Point{col, WallRow}

	giveTurn(t, s, SideAttackerA)
	victim := s.spawn(SideDefender, UnitGarrison, at)
	cat := s.spawn(SideAttackerA, UnitCatapult, Point{col, WallRow + 4})
	cat.movedTurn, cat.actedTurn = 0, 0

	hp := victim.HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: at}); err != nil {
		t.Fatalf("навесной удар: %v", err)
	}
	if victim.HP >= hp {
		t.Fatalf("накрытие не задело отряд в проломе: %d -> %d", hp, victim.HP)
	}
}

// Рыцарь рубит людей, а не камень: ни стену, ни постройки он не ломает.
func TestKnightDoesNotDamageWalls(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 3
	kn := s.spawn(SideAttackerA, UnitKnight, Point{col, WallRow + 1})
	kn.movedTurn, kn.actedTurn = 0, 0

	if Spec(UnitKnight).VsWall != 0 {
		t.Fatalf("у рыцаря множитель по стене %d, ждали 0", Spec(UnitKnight).VsWall)
	}
	before := s.segmentCol(col).HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: kn.ID, To: Point{col, WallRow}}); err == nil {
		t.Fatal("рыцарь не должен бить стену")
	}
	if s.segmentCol(col).HP != before {
		t.Fatal("стена всё-таки получила урон от рыцаря")
	}
	if containsPoint(s.AttackTargets(kn), Point{col, WallRow}) {
		t.Fatal("стена не должна быть в списке целей рыцаря")
	}
	// По людям он при этом бьёт как раньше.
	def := s.spawn(SideDefender, UnitGarrison, Point{col + 1, WallRow + 1})
	if !containsPoint(s.AttackTargets(kn), def.Pos) {
		t.Fatal("по живой цели рыцарь бить обязан")
	}
}

// Король: только рукопашная, только двор, и его смерть заканчивает партию.
func TestKingIsMeleeOnly(t *testing.T) {
	s := newTestGame()
	king := s.King(SideDefender)
	if king == nil {
		t.Fatal("король должен появляться в начале осады")
	}
	king.Pos = Point{GateColLeft, CourtRowLast}

	giveTurn(t, s, SideAttackerA)
	// Стрелок рядом — и всё равно не достаёт: клинка у него нет.
	shooter := s.spawn(SideAttackerA, UnitScout, king.Pos.Add(0, 1))
	shooter.movedTurn, shooter.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: shooter.ID, To: king.Pos}); err == nil {
		t.Fatal("стрелок не должен доставать короля")
	}
	if containsPoint(s.AttackTargets(shooter), king.Pos) {
		t.Fatal("король не должен быть целью стрелка")
	}

	// Катапульта накрывает двор — королю всё равно.
	cat := s.spawn(SideAttackerA, UnitCatapult, Point{king.Pos.X, WallRow + 3})
	cat.movedTurn, cat.actedTurn = 0, 0
	hp := king.HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: king.Pos}); err != nil {
		t.Fatalf("навесной удар: %v", err)
	}
	if king.HP != hp {
		t.Fatalf("камень достал короля: %d -> %d", hp, king.HP)
	}

	// А пехота вплотную — достаёт, и это конец партии.
	inf := s.spawn(SideAttackerA, UnitInfantry, king.Pos.Add(1, 1))
	inf.movedTurn, inf.actedTurn = 0, 0
	king.HP = 1
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: inf.ID, To: king.Pos}); err != nil {
		t.Fatalf("удар по королю: %v", err)
	}
	if s.Phase != PhaseOver {
		t.Fatal("смерть короля должна заканчивать партию")
	}
	if !s.Result.Captured || s.Result.CapturedBy != SideAttackerA.String() {
		t.Fatalf("партия закрыта неверно: %+v", s.Result)
	}
}

// Короля можно двигать, но только внутри замка.
func TestKingWalksOnlyInsideCastle(t *testing.T) {
	s := newTestGame()
	king := s.King(SideDefender)
	s.segmentCol(GateColLeft).HP = 0 // пролом в воротах: выход как будто открыт

	moves := s.Reachable(king)
	if len(moves) == 0 {
		t.Fatal("король должен уметь ходить")
	}
	for _, p := range moves {
		if !IsInsideCastle(p) {
			t.Fatalf("король дошёл до %v (%s) — за стену его пускать нельзя", p, TileAt(p))
		}
	}
	// И шаг в пролом сервер не примет.
	if err := s.Apply(SideDefender, Action{Kind: ActMove, UnitID: king.ID, To: Point{GateColLeft, WallRow}}); err == nil {
		t.Fatal("король не должен выходить в пролом")
	}
	// Обычный шаг по двору — принимает.
	to := moves[0]
	if err := s.Apply(SideDefender, Action{Kind: ActMove, UnitID: king.ID, To: to}); err != nil {
		t.Fatalf("шаг короля по двору: %v", err)
	}
	if king.Pos != to {
		t.Fatalf("король не сдвинулся: %v", king.Pos)
	}
}

// Короля нельзя нанять: он не наёмник, а приз осады.
func TestKingIsNotForSale(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = 500
	s.silver[SideDefender] = 500
	if err := s.Apply(SideDefender, Action{Kind: ActDeploy, Unit: "king", To: Point{GateColLeft, WalkRow}}); err == nil {
		t.Fatal("короля нанимать нельзя")
	}
	for _, b := range Buyable(SideDefender) {
		if b == "king" {
			t.Fatal("король не должен быть в списке покупок")
		}
	}
}

// Отстраивать стену можно не больше двух раз за ход — даже если припасов
// хватает на третий пролом.
func TestWallRebuildLimitPerTurn(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = RebuildCost * 10
	cols := []int{WallFirstCol + 7, WallFirstCol + 8, WallFirstCol + 9}

	if got := s.RepairsLeft(SideDefender); got != MaxWallRepairs {
		t.Fatalf("в начале хода ждали %d отстроек, есть %d", MaxWallRepairs, got)
	}
	for i := 0; i < MaxWallRepairs; i++ {
		s.segmentCol(cols[i]).HP = 0
		if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: cols[i]}); err != nil {
			t.Fatalf("отстройка %d: %v", i+1, err)
		}
	}
	s.segmentCol(cols[MaxWallRepairs]).HP = 0
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: cols[MaxWallRepairs]}); err == nil {
		t.Fatal("третья отстройка за ход должна быть запрещена")
	}

	// На следующем ходу счёт начинается заново.
	nextTurn(t, s, SideDefender)
	if got := s.RepairsLeft(SideDefender); got != MaxWallRepairs {
		t.Fatalf("на новом ходу ждали %d отстроек, есть %d", MaxWallRepairs, got)
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: cols[MaxWallRepairs]}); err != nil {
		t.Fatalf("отстройка на новом ходу: %v", err)
	}
}

// За один день нанимают не больше четырёх отрядов, сколько бы ни было припасов.
func TestDeployLimitPerTurn(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 500
	s.AP = 100

	spots := s.deployTiles(SideAttackerA)
	if len(spots) < MaxDeploysPerTurn+1 {
		t.Fatalf("для теста нужно хотя бы %d клеток найма", MaxDeploysPerTurn+1)
	}
	for i := 0; i < MaxDeploysPerTurn; i++ {
		if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: spots[i]}); err != nil {
			t.Fatalf("наём %d: %v", i+1, err)
		}
	}
	if got := s.HiresLeft(SideAttackerA); got != 0 {
		t.Fatalf("после %d наймов должно остаться 0, осталось %d", MaxDeploysPerTurn, got)
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: spots[MaxDeploysPerTurn]}); err == nil {
		t.Fatalf("пятый наём за ход должен быть запрещён")
	}

	nextTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 500
	if got := s.HiresLeft(SideAttackerA); got != MaxDeploysPerTurn {
		t.Fatalf("на новом ходу ждали %d наймов, есть %d", MaxDeploysPerTurn, got)
	}
}

// Рудник в углу двора: замок получает серебро, не выходя за стену, но и
// держать на нём кого-то должен постоянно.
func TestCastleMinePaysDefender(t *testing.T) {
	p := CastleMine()
	if TileAt(p) != TileMine {
		t.Fatalf("угол двора должен быть рудником, а он %s", TileAt(p))
	}
	if !IsInsideCastle(Point{p.X + 1, p.Y}) {
		t.Fatal("рудник должен стоять в углу двора, а не снаружи")
	}

	s := newTestGame()
	s.spawn(SideDefender, UnitGarrison, p)
	before := s.Silver(SideDefender)
	nextTurn(t, s, SideDefender)
	if got := s.Silver(SideDefender); got != before+SilverPerTile {
		t.Fatalf("за рудник во дворе ждали +%d серебра, получили %d", SilverPerTile, got-before)
	}
	// Застроить его нельзя: это не двор.
	if s.courtBuildable(p) {
		t.Fatal("на руднике во дворе строить нельзя")
	}
}

// Обзор: пехота ослепла, разведчик стал глазами стороны.
func TestScoutSeesFurtherThanFoot(t *testing.T) {
	foot := Spec(UnitInfantry).Vision
	scout := Spec(UnitScout).Vision
	if foot != VisionFoot {
		t.Fatalf("у пехоты обзор %d, ждали %d", foot, VisionFoot)
	}
	if scout < foot*2 {
		t.Fatalf("разведчик должен видеть минимум вдвое дальше пехоты: %d против %d", scout, foot)
	}
	if Spec(UnitScout).Label != "Разведчик" {
		t.Fatalf("лучник должен был стать разведчиком, а он %q", Spec(UnitScout).Label)
	}
	for _, tp := range []UnitType{UnitGarrison, UnitKnight, UnitEngineer, UnitHealer, UnitBomber} {
		if got := Spec(tp).Vision; got > VisionFoot {
			t.Fatalf("%s видит на %d — это больше пехотного %d", Spec(tp).Label, got, VisionFoot)
		}
	}

	s := newTestGame()
	spot := openArea(t, s, 7)
	sc := s.spawn(SideAttackerA, UnitScout, spot)
	vis := s.VisibleFor(SideAttackerA)
	if !vis[spot.Add(scout, 0)] {
		t.Fatalf("разведчик должен просматривать %d клеток", scout)
	}
	s.removeUnit(sc)
	s.spawn(SideAttackerA, UnitInfantry, spot)
	if s.VisibleFor(SideAttackerA)[spot.Add(foot+1, 0)] {
		t.Fatal("пехота не должна видеть дальше своего радиуса")
	}
}

// --- вторая правка баланса ---

// Подрывник выбирает, какой из соседних сегментов ронять: вплотную к нему
// их может быть несколько, и это решение игрока.
func TestBomberPicksSegment(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 6
	bomb := s.spawn(SideAttackerA, UnitBomber, Point{col, WallRow + 1})
	bomb.movedTurn, bomb.actedTurn = 0, 0

	targets := s.chargeTargets(bomb)
	if len(targets) != 3 {
		t.Fatalf("вплотную к подрывнику три сегмента, найдено %d: %v", len(targets), targets)
	}
	pick := Point{col + 1, WallRow} // роняем соседний, а не тот, под которым стоим
	if !containsPoint(targets, pick) {
		t.Fatalf("сосед справа должен быть целью: %v", targets)
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActCharge, UnitID: bomb.ID, To: pick}); err != nil {
		t.Fatalf("закладка заряда: %v", err)
	}
	if !s.segmentCol(col + 1).Breached() {
		t.Fatal("выбранный сегмент должен быть снесён")
	}
	if s.segmentCol(col).Breached() {
		t.Fatal("сегмент под ногами трогать не просили")
	}
}

// Подрывник живёт ровно до первого попадания.
func TestBomberHasOneHitPoint(t *testing.T) {
	if got := Spec(UnitBomber).MaxHP; got != 1 {
		t.Fatalf("у подрывника %d HP, ждали 1", got)
	}
	if got := Spec(UnitBomber).Move; got != 3 {
		t.Fatalf("у подрывника шаг %d, ждали 3", got)
	}
}

// Кладку ломают только осадные машины: ни пехота, ни рыцари, ни стрелки.
func TestOnlySiegeEnginesBreakWalls(t *testing.T) {
	siege := map[UnitType]bool{UnitRam: true, UnitCatapult: true}
	for tp := UnitType(0); tp < unitTypeCount; tp++ {
		spec := Spec(tp)
		if siege[tp] {
			if spec.VsWall <= 0 {
				t.Fatalf("%s обязан ломать стену", spec.Label)
			}
			continue
		}
		if spec.VsWall > 0 {
			t.Fatalf("%s не должен ломать стену (VsWall=%d)", spec.Label, spec.VsWall)
		}
	}

	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 3
	at := Point{col, WallRow}
	for _, tp := range []UnitType{UnitInfantry, UnitKnight, UnitScout} {
		u := s.spawn(SideAttackerA, tp, Point{col, WallRow + 2})
		u.movedTurn, u.actedTurn = 0, 0
		before := s.segmentCol(col).HP
		if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: u.ID, To: at}); err == nil {
			t.Fatalf("%s пробил стену", Spec(tp).Label)
		}
		if s.segmentCol(col).HP != before {
			t.Fatalf("%s всё-таки повредил стену", Spec(tp).Label)
		}
		if containsPoint(s.AttackTargets(u), at) {
			t.Fatalf("стена не должна быть целью для %s", Spec(tp).Label)
		}
		s.removeUnit(u)
	}
}

// Лестница: приставил к стене — наверх лезет любой пеший отряд, и в тот же
// ход, в который подошёл. Машины остаются внизу.
func TestLadderLetsAnyFootUnitUp(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 2
	// Расчищаем боевой ход: на стене должно быть куда встать.
	for _, u := range append([]*Unit{}, s.Units...) {
		if u.Owner == SideDefender && u.Pos.Y == WalkRow {
			s.removeUnit(u)
		}
	}
	s.spawn(SideAttackerA, UnitLadder, Point{col, WallRow + 1})

	for _, tp := range []UnitType{UnitInfantry, UnitHealer, UnitKnight} {
		u := s.spawn(SideAttackerA, tp, Point{col + 1, WallRow + 1})
		u.movedTurn = s.turnCounter // уже сходил в этом ходу — лезть всё равно может
		u.actedTurn = 0
		if !s.canClimb(u) {
			t.Fatalf("%s должен уметь лезть по лестнице", Spec(tp).Label)
		}
		if err := s.Apply(SideAttackerA, Action{Kind: ActClimb, UnitID: u.ID}); err != nil {
			t.Fatalf("подъём %s: %v", Spec(tp).Label, err)
		}
		if u.Pos != (Point{col, WalkRow}) {
			t.Fatalf("%s оказался в %v, ждали боевой ход", Spec(tp).Label, u.Pos)
		}
		s.removeUnit(u)
	}

	// А машине наверх нечем.
	cat := s.spawn(SideAttackerA, UnitCatapult, Point{col + 1, WallRow + 1})
	cat.movedTurn, cat.actedTurn = 0, 0
	if s.canClimb(cat) {
		t.Fatal("катапульта не лезет по лестнице")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActClimb, UnitID: cat.ID}); err == nil {
		t.Fatal("катапульта не должна подниматься на стену")
	}
}

// Смолу льют вниз, под стену, сплошной линией: ворота из неё не выпадают.
func TestPitchPoursBelowTheWall(t *testing.T) {
	plain := WallFirstCol + 2
	for _, col := range []int{plain, GateColLeft, GateColRight, WallLastCol} {
		if got := PitchTile(col); got != (Point{col, WallRow + 1}) {
			t.Fatalf("смола из столбца %d льётся в %v, а должна под стену", col, got)
		}
	}
	tiles := PitchTiles()
	if len(tiles) != Classic.Segments() {
		t.Fatalf("клеток для смолы %d, сегментов %d", len(tiles), Classic.Segments())
	}
	for _, p := range tiles {
		if p.Y != WallRow+1 {
			t.Fatalf("линия смолы разорвана: клетка %v не под стеной", p)
		}
	}

	s := newTestGame()
	s.supplies[SideDefender] = 50
	// Под стеной — штурмующий, в самой кладке никого.
	victim := s.spawn(SideAttackerA, UnitInfantry, Point{plain, WallRow + 1})
	hp := victim.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "pitch", Col: plain}); err != nil {
		t.Fatalf("смола: %v", err)
	}
	if victim.HP != hp-PitchDamage {
		t.Fatalf("смола не достала цель под стеной: %d -> %d", hp, victim.HP)
	}
}

// Стрелок на башне бьёт тяжелее, видит дальше и достаёт дальше.
func TestTowerBoostsRangedDamage(t *testing.T) {
	s := newTestGame()
	tower := addWatchPost(s, SideNeutral, openArea(t, s, 2))

	for _, tp := range []UnitType{UnitScout, UnitCrossbow} {
		inside := s.spawn(SideAttackerA, tp, tower.Pos)
		if got, want := s.unitDamage(inside), Spec(tp).Damage+TowerDamageBonus; got != want {
			t.Fatalf("%s в башне бьёт на %d, ждали %d", Spec(tp).Label, got, want)
		}
		if got, want := s.unitVision(inside), Spec(tp).Vision+TowerVisionBonus; got != want {
			t.Fatalf("%s в башне видит на %d, ждали %d", Spec(tp).Label, got, want)
		}
		if got, want := s.unitRange(inside), Spec(tp).Range+TowerGarrisonBonus; got != want {
			t.Fatalf("%s в башне достаёт на %d, ждали %d", Spec(tp).Label, got, want)
		}
		s.removeUnit(inside)
	}

	// Рукопашному отряду башня урона не добавляет: сверху мечом не достать.
	melee := s.spawn(SideAttackerA, UnitInfantry, tower.Pos)
	if got, want := s.unitDamage(melee), Spec(UnitInfantry).Damage; got != want {
		t.Fatalf("пехота в башне бьёт на %d, ждали %d", got, want)
	}
}

// Обзор башни урезан: она больше не просматривает полполя.
func TestWatchTowerVisionIsTrimmed(t *testing.T) {
	if VisionTower != 4 {
		t.Fatalf("обзор башни %d, ждали 4", VisionTower)
	}
	s := newTestGame()
	tower := addWatchPost(s, SideNeutral, openArea(t, s, VisionTower+1))
	s.spawn(SideAttackerA, UnitInfantry, tower.Pos)
	vis := s.VisibleFor(SideAttackerA)
	if !vis[tower.Pos.Add(VisionTower, 0)] {
		t.Fatalf("башня должна просматривать %d клеток", VisionTower)
	}
	if vis[tower.Pos.Add(VisionTower+1, 0)] {
		t.Fatal("башня видит дальше своего радиуса")
	}
}

// Короля с первого дня прикрывают двое храмовников: без них приз достаётся
// первому же отряду, добежавшему до пролома.
func TestKingStartsWithTemplarGuard(t *testing.T) {
	s := newTestGame()
	king := s.King(SideDefender)
	if king == nil {
		t.Fatal("король должен быть на поле")
	}
	var guards []*Unit
	for _, u := range s.Units {
		if u.Owner == SideDefender && u.Type == UnitTemplar && Chebyshev(u.Pos, king.Pos) == 1 {
			guards = append(guards, u)
		}
	}
	if len(guards) != 2 {
		t.Fatalf("при короле %d храмовников, ждали 2", len(guards))
	}

	// Стража крепкая, но бьёт слабо — это щит, а не меч.
	spec := Spec(UnitTemplar)
	if spec.MaxHP != 25 || spec.Damage != 2 {
		t.Fatalf("храмовник: %d HP, урон %d — ждали 25 и 2", spec.MaxHP, spec.Damage)
	}
	// И за стену её не выпускают.
	s.segmentCol(GateColLeft).HP = 0
	for _, p := range s.Reachable(guards[0]) {
		if !IsInsideCastle(p) {
			t.Fatalf("храмовник дошёл до %v (%s) — стража не покидает замок", p, TileAt(p))
		}
	}
	// И купить её нельзя: она не наёмная.
	for _, b := range Buyable(SideDefender) {
		if b == "templar" || b == "militia" {
			t.Fatalf("%s не должен продаваться", b)
		}
	}
}

// Требушет замка: та же машина, что катапульта, но вкопанная и одна.
func TestTrebuchetIsImmobileAndUnique(t *testing.T) {
	treb, cat := Spec(UnitTrebuchet), Spec(UnitCatapult)
	if treb.Damage != cat.Damage || treb.Range != cat.Range || treb.Splash != cat.Splash || treb.MaxHP != cat.MaxHP {
		t.Fatalf("требушет должен повторять катапульту: %+v против %+v", treb, cat)
	}
	if treb.Move != 0 {
		t.Fatalf("требушет не ездит, а шаг у него %d", treb.Move)
	}
	if treb.MaxAlive != 1 {
		t.Fatalf("требушетов не больше одного, а предел %d", treb.MaxAlive)
	}

	s := newTestGame()
	s.supplies[SideDefender] = 500
	at := Point{WallFirstCol + 4, CourtRowLast}
	if err := s.Apply(SideDefender, Action{Kind: ActDeploy, Unit: "trebuchet", To: at}); err != nil {
		t.Fatalf("наём требушета: %v", err)
	}
	treb2 := s.unitAt(at)
	if len(s.Reachable(treb2)) != 0 {
		t.Fatal("требушет не должен никуда ходить")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActDeploy, Unit: "trebuchet", To: at.Add(2, 0)}); err == nil {
		t.Fatal("второй требушет держать нельзя")
	}
	// И осаждающим он недоступен.
	for _, u := range Buyable(SideAttackerA) {
		if u == "trebuchet" {
			t.Fatal("требушет — машина замка")
		}
	}
}

// Разведчик бегает быстрее любой пехоты, лестница успевает доехать до стены.
func TestScoutAndLadderSpeeds(t *testing.T) {
	if got := Spec(UnitScout).Move; got != 4 {
		t.Fatalf("у разведчика шаг %d, ждали 4", got)
	}
	if got := Spec(UnitLadder).Move; got != 3 {
		t.Fatalf("у лестницы шаг %d, ждали 3", got)
	}
}

// Арбалетчики ходят наравне с пехотой и видят наравне с разведчиком: это
// элита, и платят за неё серебром.
func TestCrossbowMovesAndSeesLikeScout(t *testing.T) {
	cb, scout := Spec(UnitCrossbow), Spec(UnitScout)
	if cb.Move != 3 {
		t.Fatalf("у арбалетчиков шаг %d, ждали 3", cb.Move)
	}
	if cb.Vision != scout.Vision {
		t.Fatalf("обзор арбалетчиков %d, у разведчика %d — должны совпадать", cb.Vision, scout.Vision)
	}

	s := newTestGame()
	clearTowers(s)
	spot := openArea(t, s, cb.Vision+1)
	u := s.spawn(SideAttackerA, UnitCrossbow, spot)
	vis := s.VisibleFor(SideAttackerA)
	if !vis[spot.Add(cb.Vision, 0)] {
		t.Fatalf("арбалетчики должны просматривать %d клеток", cb.Vision)
	}
	if len(s.Reachable(u)) == 0 {
		t.Fatal("арбалетчики должны ходить")
	}
}

// --- засада возвращается ---

// Регрессия: spotted работало защёлкой в одну сторону. Отряд, который хоть
// раз засветился, оставался видимым в кустах навсегда — даже когда вокруг не
// было ни одного вражеского глаза.
func TestAmbushReturnsWhenEnemyLosesSight(t *testing.T) {
	s := newTestGame()
	clearTowers(s)
	bush, _, _ := bushPair(t)
	for _, u := range append([]*Unit{}, s.Units...) {
		s.removeUnit(u) // убираем чужие глаза с карты
	}
	u := s.spawn(SideAttackerA, UnitInfantry, bush)
	u.revealAll() // «меня засекли»

	if s.VisibleFor(SideDefender)[bush] {
		t.Fatal("для теста замок не должен просматривать этот куст")
	}
	if s.concealed(u, SideDefender) {
		t.Fatal("засветившийся отряд не в засаде — пока ход не сменился")
	}

	s.endTurn()
	if !s.concealed(u, SideDefender) {
		t.Fatal("враг потерял отряд из виду — заросли должны снова прятать")
	}

	// А вот под наблюдением скрытность не возвращается: дозорный рядом
	// держит куст на виду.
	eye := s.spawn(SideDefender, UnitScout, bush.Add(0, -1))
	u.revealAll()
	s.endTurn()
	if s.concealed(u, SideDefender) {
		t.Fatal("пока за кустом следят, засада не восстанавливается")
	}
	s.removeUnit(eye)
	s.endTurn()
	if !s.concealed(u, SideDefender) {
		t.Fatal("наблюдатель ушёл — засада снова работает")
	}
}

// Пересчёт только возвращает скрытность, но никогда её не снимает: смотреть
// на куст в упор по-прежнему бесполезно.
func TestCoverRefreshNeverReveals(t *testing.T) {
	s := newTestGame()
	clearTowers(s)
	bush, _, _ := bushPair(t)
	u := s.spawn(SideAttackerA, UnitInfantry, bush)
	u.spotted = [4]bool{} // сидит в засаде
	s.spawn(SideDefender, UnitScout, bush.Add(0, -1))

	if !s.concealed(u, SideDefender) {
		t.Fatal("отряд должен быть в засаде")
	}
	s.endTurn()
	if !s.concealed(u, SideDefender) {
		t.Fatal("пересчёт не должен вскрывать засаду")
	}
}

// --- осадная башня ---

// Ход ромбом: прямо на две клетки, по диагонали на одну.
func TestSiegeTowerMovesInDiamond(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	centre := openArea(t, s, 4)
	tower := s.spawn(SideAttackerA, UnitSiegeTower, centre)
	tower.movedTurn, tower.actedTurn = 0, 0

	reach := map[Point]bool{}
	for _, p := range s.Reachable(tower) {
		reach[p] = true
	}
	for _, want := range []Point{{centre.X + 2, centre.Y}, {centre.X - 2, centre.Y},
		{centre.X, centre.Y + 2}, {centre.X, centre.Y - 2},
		{centre.X + 1, centre.Y + 1}, {centre.X - 1, centre.Y - 1}} {
		if !reach[want] {
			t.Fatalf("клетка %v должна быть достижима ромбом", want)
		}
	}
	for _, no := range []Point{{centre.X + 2, centre.Y + 1}, {centre.X + 2, centre.Y + 2},
		{centre.X - 2, centre.Y + 2}} {
		if reach[no] {
			t.Fatalf("клетка %v выходит за ромб, а достижима", no)
		}
	}
}

// Посадка, трап, высадка на боевой ход — весь путь через целую стену.
func TestSiegeTowerCarriesUnitsOntoTheWall(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 6
	at := Point{col, WallRow + 1}
	// Расчищаем боевой ход: высаживаться должно быть куда.
	for _, u := range append([]*Unit{}, s.Units...) {
		if u.Owner == SideDefender && u.Pos.Y == WalkRow {
			s.removeUnit(u)
		}
	}
	tower := s.spawn(SideAttackerA, UnitSiegeTower, at)
	tower.movedTurn, tower.actedTurn = 0, 0

	// Сажаем четверых — пятому места нет.
	var riders []*Unit
	for i := 0; i < SiegeTowerCap+1; i++ {
		u := s.spawn(SideAttackerA, UnitInfantry, at.Add(0, 1))
		u.movedTurn, u.actedTurn = 0, 0
		err := s.Apply(SideAttackerA, Action{Kind: ActBoard, UnitID: u.ID, To: at})
		if i < SiegeTowerCap {
			if err != nil {
				t.Fatalf("посадка %d: %v", i+1, err)
			}
			riders = append(riders, u)
			continue
		}
		if err == nil {
			t.Fatalf("в башню влезает только %d отрядов", SiegeTowerCap)
		}
		s.removeUnit(u)
	}
	if got := len(s.cargoOf(tower)); got != SiegeTowerCap {
		t.Fatalf("внутри %d отрядов, ждали %d", got, SiegeTowerCap)
	}
	// Пассажира нет на карте: клетку он не занимает и целью не является.
	if u := s.unitAt(at); u != tower {
		t.Fatalf("на клетке башни должна стоять башня, а стоит %v", u)
	}
	if s.VisibleUnit(riders[0], SideDefender) {
		t.Fatal("пассажира защитник видеть не должен")
	}

	// Пока трап не опущен, сойти нельзя.
	target := Point{col, WalkRow}
	if err := s.Apply(SideAttackerA, Action{Kind: ActUnload, UnitID: riders[0].ID, To: target}); err == nil {
		t.Fatal("без трапа высаживаться некуда")
	}

	if err := s.Apply(SideAttackerA, Action{Kind: ActRamp, UnitID: tower.ID}); err != nil {
		t.Fatalf("трап: %v", err)
	}
	if !tower.Ramp {
		t.Fatal("трап должен быть опущен")
	}
	// Стена при этом целая — в том и смысл башни.
	if s.segmentCol(col).Breached() {
		t.Fatal("для башни пролом не нужен")
	}

	nextTurn(t, s, SideAttackerA)
	if err := s.Apply(SideAttackerA, Action{Kind: ActUnload, UnitID: riders[0].ID, To: target}); err != nil {
		t.Fatalf("высадка: %v", err)
	}
	if riders[0].Pos != target || riders[0].Carrier != 0 {
		t.Fatalf("после высадки отряд в %v (carrier %d)", riders[0].Pos, riders[0].Carrier)
	}
	if got := len(s.cargoOf(tower)); got != SiegeTowerCap-1 {
		t.Fatalf("внутри осталось %d, ждали %d", got, SiegeTowerCap-1)
	}
}

// Трап открывается только вплотную к стене и складывается на первом же шаге.
func TestSiegeTowerRampNeedsTheWall(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	far := s.spawn(SideAttackerA, UnitSiegeTower, Point{WallFirstCol + 6, WallRow + 4})
	far.movedTurn, far.actedTurn = 0, 0
	if s.canRamp(far) {
		t.Fatal("издали трап перекинуть не на что")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActRamp, UnitID: far.ID}); err == nil {
		t.Fatal("трап вдали от стены должен быть запрещён")
	}
	s.removeUnit(far)

	at := Point{WallFirstCol + 6, WallRow + 1}
	tower := s.spawn(SideAttackerA, UnitSiegeTower, at)
	tower.movedTurn, tower.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActRamp, UnitID: tower.ID}); err != nil {
		t.Fatalf("трап у стены: %v", err)
	}

	// Шаг в сторону — трап складывается, экипаж едет с башней.
	rider := s.spawn(SideAttackerA, UnitInfantry, at.Add(0, 1))
	rider.Carrier = tower.ID
	rider.Pos = tower.Pos
	nextTurn(t, s, SideAttackerA)
	to := at.Add(0, 1)
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: tower.ID, To: to}); err != nil {
		t.Fatalf("шаг башни: %v", err)
	}
	if tower.Ramp {
		t.Fatal("на ходу трап держать нельзя")
	}
	if rider.Pos != to {
		t.Fatalf("экипаж остался в %v, а башня уехала в %v", rider.Pos, to)
	}
}

// Разбитая башня хоронит экипаж: половина полного здоровья каждому.
func TestWreckedSiegeTowerHurtsItsCrew(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	at := openArea(t, s, 3)
	tower := s.spawn(SideAttackerA, UnitSiegeTower, at)

	// Рыцарь (18 HP) с шестью единицами — такой в обломках гибнет.
	doomed := s.spawn(SideAttackerA, UnitKnight, at)
	doomed.Carrier, doomed.HP = tower.ID, 6
	// Здоровая пехота (10 HP) переживает пять урона и спрыгивает.
	lucky := s.spawn(SideAttackerA, UnitInfantry, at)
	lucky.Carrier = tower.ID

	if got := wreckDamage(doomed); got != (Spec(UnitKnight).MaxHP+1)/2 {
		t.Fatalf("урон от обломков %d", got)
	}
	s.killUnit(SideDefender, tower)

	if s.unitByID(doomed.ID) != nil {
		t.Fatalf("раненый рыцарь должен погибнуть в обломках (было %d HP)", 6)
	}
	alive := s.unitByID(lucky.ID)
	if alive == nil {
		t.Fatal("целая пехота должна выбраться")
	}
	if want := Spec(UnitInfantry).MaxHP - wreckDamage(lucky); alive.HP != want {
		t.Fatalf("у выжившего %d HP, ждали %d", alive.HP, want)
	}
	if alive.Carrier != 0 {
		t.Fatal("выживший больше ни в какой башне не сидит")
	}
	if s.unitAt(alive.Pos) != alive {
		t.Fatalf("выживший должен занять клетку, а он в %v", alive.Pos)
	}
}

// Машины внутрь не грузят, и чужую башню занять нельзя.
func TestSiegeTowerBoardingLimits(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	at := openArea(t, s, 3)
	tower := s.spawn(SideAttackerA, UnitSiegeTower, at)
	tower.movedTurn, tower.actedTurn = 0, 0

	cat := s.spawn(SideAttackerA, UnitCatapult, at.Add(1, 0))
	cat.movedTurn, cat.actedTurn = 0, 0
	if len(s.boardTargets(cat)) != 0 {
		t.Fatal("катапульту в башню не грузят")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActBoard, UnitID: cat.ID, To: at}); err == nil {
		t.Fatal("машина не должна садиться в башню")
	}

	rival := s.spawn(SideAttackerB, UnitInfantry, at.Add(0, 1))
	rival.movedTurn, rival.actedTurn = 0, 0
	if len(s.boardTargets(rival)) != 0 {
		t.Fatal("в чужую башню не садятся")
	}
}

// Король стал хрупким: 10 прочности и двойка урона. Приз должен быть тем,
// что защищают, а не тем, что само отбивается.
func TestKingIsFragile(t *testing.T) {
	spec := Spec(UnitKing)
	if spec.MaxHP != 10 || spec.Damage != 2 {
		t.Fatalf("король: %d HP, урон %d — ждали 10 и 2", spec.MaxHP, spec.Damage)
	}
	if !spec.Bound || !spec.MeleeOnly || !spec.Royal {
		t.Fatalf("король потерял свои свойства: %+v", spec)
	}
}

// Ополчение: трое за одно действие, мимо предела армии, раз в ход.
func TestMilitiaCallUp(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = MilitiaCost * 3

	before := s.ArmySize(SideDefender)
	if !s.MilitiaReady() {
		t.Fatal("ополчение должно быть доступно")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "militia"}); err != nil {
		t.Fatalf("призыв: %v", err)
	}

	var levy []*Unit
	for _, u := range s.Units {
		if u.Type == UnitMilitia {
			levy = append(levy, u)
		}
	}
	if len(levy) != MilitiaCount {
		t.Fatalf("призвано %d горожан, ждали %d", len(levy), MilitiaCount)
	}
	for _, u := range levy {
		if !IsInsideCastle(u.Pos) {
			t.Fatalf("ополченец встал в %v — это не замок", u.Pos)
		}
		if u.Spec().MaxHP != 8 || u.Spec().Damage != 3 {
			t.Fatalf("ополченец: %d HP, урон %d — ждали 8 и 3", u.Spec().MaxHP, u.Spec().Damage)
		}
	}
	// Главное: в общий реестр они не идут.
	if got := s.ArmySize(SideDefender); got != before {
		t.Fatalf("предел армии вырос с %d до %d — ополчение в него не входит", before, got)
	}
	if got := s.Supplies(SideDefender); got != MilitiaCost*3-MilitiaCost {
		t.Fatalf("списано %d припасов, ждали %d", MilitiaCost*3-got, MilitiaCost)
	}

	// Дважды за ход — нельзя.
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "militia"}); err == nil {
		t.Fatal("ополчение созывают раз в ход")
	}
	// И на следующем ходу тоже: трое уже стоят, потолок выбран.
	nextTurn(t, s, SideDefender)
	if s.MilitiaReady() {
		t.Fatal("при полном поле кнопка призыва должна гаснуть")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "militia"}); err == nil {
		t.Fatalf("ополченцев уже %d — призыв должен быть отбит", MaxMilitia)
	}

	// И только замку.
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 500
	if err := s.Apply(SideAttackerA, Action{Kind: ActAbility, Ability: "militia"}); err == nil {
		t.Fatal("осаждающие ополчение не созывают")
	}
}

// Предел ополчения: больше MaxMilitia на поле не бывает, а призыв добирает
// до потолка, а не выкладывает каждый раз полную тройку.
func TestMilitiaAliveCap(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = MilitiaCost * 10

	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "militia"}); err != nil {
		t.Fatalf("первый призыв: %v", err)
	}
	if got := s.countUnits(SideDefender, UnitMilitia); got != MaxMilitia {
		t.Fatalf("после призыва ополченцев %d, ждали %d", got, MaxMilitia)
	}

	// Один пал — на его место поднимается ровно один, а не новая тройка.
	for _, u := range append([]*Unit{}, s.Units...) {
		if u.Type == UnitMilitia {
			s.removeUnit(u)
			break
		}
	}
	nextTurn(t, s, SideDefender)
	if !s.MilitiaReady() {
		t.Fatal("место освободилось — призыв должен снова стать доступен")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "militia"}); err != nil {
		t.Fatalf("добор ополчения: %v", err)
	}
	if got := s.countUnits(SideDefender, UnitMilitia); got != MaxMilitia {
		t.Fatalf("после добора ополченцев %d, ждали %d", got, MaxMilitia)
	}
}

// Кипящую смолу льют не больше MaxPitchPerTurn раз за ход, и счёт
// обнуляется со сменой хода.
func TestPitchLimitPerTurn(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = PitchCost * 20

	if got := s.PitchesLeft(SideDefender); got != MaxPitchPerTurn {
		t.Fatalf("в начале хода вёдер %d, ждали %d", got, MaxPitchPerTurn)
	}
	for i := 0; i < MaxPitchPerTurn; i++ {
		if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "pitch", Col: WallFirstCol + i}); err != nil {
			t.Fatalf("смола №%d: %v", i+1, err)
		}
	}
	if got := s.PitchesLeft(SideDefender); got != 0 {
		t.Fatalf("после %d вёдер осталось %d, ждали 0", MaxPitchPerTurn, got)
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "pitch", Col: WallFirstCol}); err == nil {
		t.Fatalf("смолу льют не больше %d раз за ход", MaxPitchPerTurn)
	}

	// Новый ход — котлы снова полны.
	nextTurn(t, s, SideDefender)
	if got := s.PitchesLeft(SideDefender); got != MaxPitchPerTurn {
		t.Fatalf("на новом ходу вёдер %d, ждали %d", got, MaxPitchPerTurn)
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "pitch", Col: WallFirstCol}); err != nil {
		t.Fatalf("смола на новом ходу: %v", err)
	}
}

// Даже упёршись в потолок армии, замок может поднять горожан.
func TestMilitiaIgnoresArmyCap(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = 500
	spot := Point{WallFirstCol + 1, CourtRowLast}
	for s.ArmySize(SideDefender) < ArmyCap(SideDefender) {
		s.spawn(SideDefender, UnitGarrison, spot)
		spot = spot.Add(1, 0) // клетки могут совпасть — для счётчика это неважно
	}
	if s.ArmySize(SideDefender) < ArmyCap(SideDefender) {
		t.Fatal("для теста нужна полная армия")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActDeploy, Unit: "garrison", To: Point{GateColLeft, WalkRow}}); err == nil {
		t.Fatal("на пределе армии наём должен быть закрыт")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "militia"}); err != nil {
		t.Fatalf("ополчение должно проходить мимо предела: %v", err)
	}
}

// Осадную башню собирают в лагере: аванпост такую махину не выкатит.
func TestSiegeTowerIsCampOnly(t *testing.T) {
	if got := Spec(UnitSiegeTower).MaxHP; got != 50 {
		t.Fatalf("у осадной башни %d HP, ждали 50", got)
	}
	if !Spec(UnitSiegeTower).CampOnly {
		t.Fatal("осадная башня должна собираться только в лагере")
	}

	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = 500
	// Ставим аванпост и проверяем, что из него башню не выкатить.
	at := openArea(t, s, 2)
	s.Buildings = append(s.Buildings, &Building{
		ID: s.nextBuildingID, Kind: BuildOutpost, Owner: SideAttackerA, Pos: at,
		HP: OutpostHP, MaxHP: OutpostHP, Expires: s.Day + OutpostLife,
	})
	s.nextBuildingID++
	near := at.Add(1, 0)
	if !s.deployZone(SideAttackerA, near) {
		t.Fatal("для теста нужна рабочая зона аванпоста")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "infantry", To: near}); err != nil {
		t.Fatalf("пехота из аванпоста выходить обязана: %v", err)
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "siegetower", To: near.Add(0, 1)}); err == nil {
		t.Fatal("осадную башню из аванпоста выкатывать нельзя")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActDeploy, Unit: "siegetower", To: Point{3, CampRowLast - 1}}); err != nil {
		t.Fatalf("в лагере башня собирается: %v", err)
	}
}

// --- ворота как механизм ---

// Решётка — состояние, а не свойство кладки: опущена — не пройдёт никто,
// поднята — пройдут все, включая осаждающего.
func TestPortcullisOpensForEveryone(t *testing.T) {
	s := newTestGame()
	col := GateColLeft
	gate := Point{col, WallRow}
	seg := s.segmentCol(col)
	if !seg.Kind.IsGate() || seg.Open {
		t.Fatal("ворота должны начинать осаду с опущенной решёткой")
	}

	// Пока решётка опущена, своя вылазка через проём не выходит.
	out := s.spawn(SideDefender, UnitGarrison, Point{col, WalkRow})
	if containsPoint(s.Reachable(out), gate) {
		t.Fatal("закрытые ворота пропустили защитника")
	}

	ap := s.AP
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "gate", Col: col}); err != nil {
		t.Fatalf("поднять решётку: %v", err)
	}
	if !s.segmentCol(col).Open || s.AP != ap-GateAPCost {
		t.Fatalf("решётка %v, очков %d из %d", s.segmentCol(col).Open, s.AP, ap)
	}
	if !containsPoint(s.Reachable(out), gate) {
		t.Fatal("в поднятую решётку защитник должен проходить")
	}

	// И осаждающий — тоже: в этом вся цена вылазки.
	giveTurn(t, s, SideAttackerA)
	s.fillMoatAt(Point{col, MoatRow}, SideAttackerA) // ров на пути засыпан, остаётся сама решётка
	raider := s.spawn(SideAttackerA, UnitInfantry, Point{col, MoatRow})
	raider.movedTurn, raider.actedTurn = 0, 0
	if !containsPoint(s.Reachable(raider), gate) {
		t.Fatal("открытые ворота обязаны пускать и штурмующих")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: raider.ID, To: gate}); err != nil {
		t.Fatalf("шаг в открытые ворота: %v", err)
	}

	// Опустить решётку на голову стоящему в проёме нельзя.
	giveTurn(t, s, SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "gate", Col: col}); err == nil {
		t.Fatal("решётка опустилась на занятый проём")
	}
	s.removeUnit(raider)
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "gate", Col: col}); err != nil {
		t.Fatalf("опустить решётку: %v", err)
	}
	if s.segmentCol(col).Open {
		t.Fatal("решётка должна закрыться")
	}
}

// --- ров и фашины ---

// В открытую воду не входит никто: ни пеший вброд, ни колёсное, ни те, кого
// несут на руках. Единственная дорога через ров — настил из фашин.
func TestMoatStopsEveryone(t *testing.T) {
	s := newTestGame()
	col := WallFirstCol + 3
	ditch := Point{col, MoatRow}
	if TileAt(ditch) != TileMoat {
		t.Fatalf("клетка %v должна быть рвом, а она %s", ditch, TileAt(ditch))
	}

	giveTurn(t, s, SideAttackerA)
	// Столбцы берём подальше от ворот: напротив них стоит мост, и вода там
	// закрыта настилом.
	for _, tc := range []struct {
		label string
		typ   UnitType
		col   int
	}{
		{"пехота", UnitInfantry, col},
		{"разведчик", UnitScout, col + 1},
		{"таран", UnitRam, col + 2},
		{"катапульта", UnitCatapult, col + 3},
		{"лестница", UnitLadder, col + 4},
	} {
		u := s.spawn(SideAttackerA, tc.typ, Point{tc.col, MoatRow + 1})
		u.movedTurn, u.actedTurn = 0, 0
		if containsPoint(s.Reachable(u), Point{tc.col, MoatRow}) {
			t.Fatalf("%s не должна входить в открытую воду", tc.label)
		}
	}
	ram := s.unitAt(Point{col + 2, MoatRow + 1})
	if ram == nil {
		t.Fatal("таран потерялся")
	}

	// Защитнику своя же вода тоже не брод: вылазка идёт через ворота.
	giveTurn(t, s, SideDefender)
	out := s.spawn(SideDefender, UnitGarrison, Point{col + 5, MoatRow + 1})
	out.movedTurn, out.actedTurn = 0, 0
	if containsPoint(s.Reachable(out), Point{col + 5, MoatRow}) {
		t.Fatal("защитник тоже не ходит по воде")
	}
	giveTurn(t, s, SideAttackerA)
	// Засыпали — и машина проходит, причём уже за одно очко шага.
	s.fillMoatAt(Point{col + 2, MoatRow}, SideAttackerA)
	if s.moatOpen(Point{col + 2, MoatRow}) {
		t.Fatal("клетка засыпана, ров на ней кончился")
	}
	ram.movedTurn, ram.actedTurn = 0, 0
	if !containsPoint(s.Reachable(ram), Point{col + 2, MoatRow}) {
		t.Fatal("по засыпанной клетке таран обязан пройти")
	}
}

// Фашины кидают по клетке за действие, и делают это отряды, а не машины.
func TestFascinesFillMoatOnePerAction(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 6
	at := Point{col, MoatRow}
	u := s.spawn(SideAttackerA, UnitInfantry, Point{col, MoatRow + 1})
	u.movedTurn, u.actedTurn = 0, 0

	if !containsPoint(s.fascineSpots(u), at) {
		t.Fatal("соседняя клетка рва должна быть доступна для фашин")
	}
	ap := s.AP
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: u.ID, Build: "fascine", To: at}); err != nil {
		t.Fatalf("фашины: %v", err)
	}
	if s.moatOpen(at) {
		t.Fatal("клетка должна быть засыпана")
	}
	// Пехоте хворост не стоит ни очка: носить вязанки — её работа.
	if s.AP != ap {
		t.Fatalf("пехота потратила очки на фашины: было %d, стало %d", ap, s.AP)
	}
	// А всем прочим — очко действия. Полевой инженер платит как все.
	sap := s.spawn(SideAttackerA, UnitSapper, Point{col - 4, MoatRow + 1})
	sap.movedTurn, sap.actedTurn = 0, 0
	ap = s.AP
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: sap.ID, Build: "fascine",
		To: Point{col - 4, MoatRow}}); err != nil {
		t.Fatalf("фашины инженером: %v", err)
	}
	if s.AP != ap-FascineAPCost {
		t.Fatalf("очков %d, ждали %d", s.AP, ap-FascineAPCost)
	}
	// Второй раз тем же отрядом в этом ходу — нельзя: по клетке за действие.
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: u.ID, Build: "fascine",
		To: Point{col + 1, MoatRow}}); err == nil {
		t.Fatal("отряд уже бросал фашины в этом ходу")
	}

	// Машине хворост не носят, и замку свой ров засыпать незачем.
	cat := s.spawn(SideAttackerA, UnitCatapult, Point{col + 2, MoatRow + 1})
	cat.movedTurn, cat.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: cat.ID, Build: "fascine",
		To: Point{col + 2, MoatRow}}); err == nil {
		t.Fatal("машина не должна забрасывать ров")
	}
	giveTurn(t, s, SideDefender)
	def := s.spawn(SideDefender, UnitGarrison, Point{col + 3, WalkRow})
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: def.ID, Build: "fascine",
		To: Point{col + 3, MoatRow}}); err == nil {
		t.Fatal("защитник не засыпает свой же ров")
	}
}

// --- ветераны ---

// Добивший чужой отряд получает звезду: +1 к урону и +1 к прочности, но не
// больше трёх звёзд.
func TestVeteranStarsGrowAndCap(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	at := openArea(t, s, 2)
	hero := s.spawn(SideAttackerA, UnitInfantry, at)
	base := Spec(UnitInfantry).Damage
	baseHP := Spec(UnitInfantry).MaxHP

	for i := 1; i <= VeteranMaxStars+2; i++ {
		victim := s.spawn(SideDefender, UnitMilitia, at.Add(1, 0))
		victim.HP = 1
		hero.actedTurn = 0
		if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: hero.ID, To: victim.Pos}); err != nil {
			t.Fatalf("удар %d: %v", i, err)
		}
		if s.unitByID(victim.ID) != nil {
			t.Fatalf("цель должна была погибнуть на ударе %d", i)
		}
		if s.AP == 0 {
			nextTurn(t, s, SideAttackerA)
		}
		want := i
		if want > VeteranMaxStars {
			want = VeteranMaxStars
		}
		if hero.Stars() != want {
			t.Fatalf("после %d убийств звёзд %d, ждали %d", i, hero.Stars(), want)
		}
		if got := s.unitDamage(hero); got != base+want*VeteranDamage {
			t.Fatalf("урон ветерана %d, ждали %d", got, base+want*VeteranDamage)
		}
		if got := hero.MaxHP(); got != baseHP+want*VeteranHP {
			t.Fatalf("прочность ветерана %d, ждали %d", got, baseHP+want*VeteranHP)
		}
	}
	// Звезда приходит вместе со здоровьем: награда не должна выглядеть раной.
	if hero.HP != baseHP+VeteranMaxStars*VeteranHP {
		t.Fatalf("у ветерана %d HP из %d", hero.HP, hero.MaxHP())
	}
}

// Ловушка и смола звёзд не дают: добивать там некому.
func TestTrapKillGivesNoStar(t *testing.T) {
	s := newTestGame()
	at := openArea(t, s, 1)
	s.Traps = append(s.Traps, Trap{Pos: at, Owner: SideDefender})
	giveTurn(t, s, SideAttackerA)
	u := s.spawn(SideAttackerA, UnitScout, at.Add(0, 1))
	u.movedTurn, u.actedTurn = 0, 0
	u.HP = 1
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: u.ID, To: at}); err != nil {
		t.Fatalf("шаг на ловушку: %v", err)
	}
	if s.unitByID(u.ID) != nil {
		t.Fatal("разведчик должен был погибнуть на ловушке")
	}
	for _, d := range s.Units {
		if d.Owner == SideDefender && d.Stars() > 0 {
			t.Fatal("ловушка выдала звезду ветерана")
		}
	}
}

// --- потолок обоза ---

// Больше потолка припасы не копятся, а склад его поднимает.
func TestSupplyCapAndWarehouse(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = SupplyCap - 1
	s.addSupplies(SideDefender, 50)
	if got := s.Supplies(SideDefender); got != SupplyCap {
		t.Fatalf("без склада в обозе %d, ждали потолок %d", got, SupplyCap)
	}

	giveTurn(t, s, SideDefender)
	spot := s.courtSpots()[0]
	s.supplies[SideDefender] = WarehouseCost
	if err := s.Apply(SideDefender, Action{Kind: ActConstruct, Build: "warehouse", To: spot}); err != nil {
		t.Fatalf("склад во дворе: %v", err)
	}
	if got, want := s.SupplyCap(SideDefender), SupplyCap+WarehouseCap; got != want {
		t.Fatalf("потолок со складом %d, ждали %d", got, want)
	}
	s.addSupplies(SideDefender, 1000)
	if got := s.Supplies(SideDefender); got != SupplyCap+WarehouseCap {
		t.Fatalf("в обозе %d, ждали %d", got, SupplyCap+WarehouseCap)
	}

	// Осаждающий ставит склад пехотой в своём лагере, а не в поле.
	giveTurn(t, s, SideAttackerA)
	s.supplies[SideAttackerA] = WarehouseCost
	inf := s.spawn(SideAttackerA, UnitInfantry, Point{2, CampRowFirst})
	inf.movedTurn, inf.actedTurn = 0, 0
	field := Point{2, CampRowFirst - 2}
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: inf.ID, Build: "warehouse", To: field}); err == nil {
		t.Fatal("склад в чистом поле ставить нельзя")
	}
	camp := Point{3, CampRowFirst}
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: inf.ID, Build: "warehouse", To: camp}); err != nil {
		t.Fatalf("склад в лагере: %v", err)
	}
	if got, want := s.SupplyCap(SideAttackerA), SupplyCap+WarehouseCap; got != want {
		t.Fatalf("потолок осаждающего %d, ждали %d", got, want)
	}
}

// Излишек над потолком пропадает и на переносе неизрасходованных очков.
func TestSupplyCapClampsCarryover(t *testing.T) {
	s := newTestGame()
	s.supplies[SideDefender] = SupplyCap
	s.endTurn() // у защитника оставались все очки хода
	if got := s.Supplies(SideDefender); got != SupplyCap {
		t.Fatalf("перенос очков пробил потолок: %d", got)
	}
}

// --- инженеры ---

// Инженеры латают стоящую кладку, чинят свои машины и не трогают пролом.
func TestEngineerRepairsWallAndEngine(t *testing.T) {
	if got := Spec(UnitEngineer).Move; got != 4 {
		t.Fatalf("инженеры должны ходить на 4 клетки, а ходят на %d", got)
	}
	s := newTestGame()
	giveTurn(t, s, SideDefender)
	col := WallFirstCol + 2
	seg := s.segmentCol(col)
	seg.HP = 5
	s.supplies[SideDefender] = 100
	eng := s.spawn(SideDefender, UnitEngineer, Point{col, WalkRow})

	supplies := s.Supplies(SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "repair",
		To: Point{col, WallRow}}); err != nil {
		t.Fatalf("ремонт кладки: %v", err)
	}
	if want := 5 + WallHP/EngineerWallShare; seg.HP != want {
		t.Fatalf("после ремонта %d, ждали %d", seg.HP, want)
	}
	if got, want := s.Supplies(SideDefender), supplies-EngineerWallCost; got != want {
		t.Fatalf("на ремонт ушло %d припасов, ждали %d", supplies-got, EngineerWallCost)
	}

	// Пролом так не поднять: для него отстройка или деревянные ворота.
	other := s.segmentCol(col + 1)
	other.HP = 0
	eng.actedTurn = 0
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "repair",
		To: Point{col + 1, WallRow}}); err == nil {
		t.Fatal("пролом не латают")
	}

	// Своя осадная машина: четверть полной прочности за раз.
	ball := s.spawn(SideDefender, UnitBallista, Point{col - 1, WalkRow})
	ball.HP = 1
	eng.actedTurn = 0
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "repair",
		To: ball.Pos}); err != nil {
		t.Fatalf("ремонт баллисты: %v", err)
	}
	if want := 1 + Spec(UnitBallista).MaxHP/EngineerEngShare; ball.HP != want {
		t.Fatalf("у баллисты %d HP, ждали %d", ball.HP, want)
	}
	// Живых инженеры не лечат — это дело лекаря.
	hurt := s.spawn(SideDefender, UnitGarrison, Point{col, CourtRowLast})
	hurt.HP = 1
	mender := s.spawn(SideDefender, UnitEngineer, Point{col + 1, CourtRowLast})
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: mender.ID, Build: "repair",
		To: hurt.Pos}); err == nil {
		t.Fatal("инженеры не перевязывают людей")
	}
}

// Деревянные ворота закрывают пролом — но не на месте замковых ворот.
func TestEngineerBoardsUpBreachWithWoodGate(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideDefender)
	col := WallFirstCol + 2
	s.segmentCol(col).HP = 0
	s.supplies[SideDefender] = 100
	eng := s.spawn(SideDefender, UnitEngineer, Point{col, WalkRow})

	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "woodgate",
		To: Point{col, WallRow}}); err != nil {
		t.Fatalf("деревянные ворота: %v", err)
	}
	seg := s.segmentCol(col)
	if seg.Kind != SegWoodGate || seg.HP != WoodGateHP || seg.Open {
		t.Fatalf("сегмент %s, %d HP, решётка %v", seg.Kind, seg.HP, seg.Open)
	}
	if seg.Passable() {
		t.Fatal("закрытая створка не должна пропускать")
	}

	// На месте замковых ворот — нельзя, даже когда там пролом.
	s.segmentCol(GateColLeft).HP = 0
	gateEng := s.spawn(SideDefender, UnitEngineer, Point{GateColLeft, WalkRow})
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: gateEng.ID, Build: "woodgate",
		To: Point{GateColLeft, WallRow}}); err == nil {
		t.Fatal("замковые ворота деревянными не подменяют")
	}

	// А отстройка возвращает именно кладку, а не створку.
	s.supplies[SideDefender] = 200
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: col}); err == nil {
		t.Fatal("отстраивают только пролом, а тут стоят ворота")
	}
	seg.HP = 0
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "rebuild", Col: col}); err != nil {
		t.Fatalf("отстройка: %v", err)
	}
	if seg.Kind != SegWall || seg.MaxHP != WallHP {
		t.Fatalf("после отстройки сегмент %s на %d", seg.Kind, seg.MaxHP)
	}
}

// --- осадная башня ---

// Трап падает на боевой ход всей массой: кто стоял на клетке — погиб.
// Короля это не берёт: его достают только клинком.
func TestRampCrushesWhoeverStandsUnderIt(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 7
	at := Point{col, MoatRow}
	for _, u := range append([]*Unit{}, s.Units...) {
		if u.Owner == SideDefender && u.Pos.Y == WalkRow {
			s.removeUnit(u)
		}
	}
	victim := s.spawn(SideDefender, UnitGarrison, Point{col, WalkRow})
	tower := s.spawn(SideAttackerA, UnitSiegeTower, at)
	tower.movedTurn, tower.actedTurn = 0, 0

	if err := s.Apply(SideAttackerA, Action{Kind: ActRamp, UnitID: tower.ID}); err != nil {
		t.Fatalf("трап: %v", err)
	}
	if s.unitByID(victim.ID) != nil {
		t.Fatal("отряд под трапом должен погибнуть")
	}
	if tower.Stars() != 1 {
		t.Fatalf("трап — тоже убийство: звёзд у башни %d", tower.Stars())
	}

	// Король под трапом выживает: падающей доской осаду не заканчивают.
	nextTurn(t, s, SideAttackerA)
	tower.Ramp, tower.actedTurn = false, 0
	king := s.King(SideDefender)
	king.Pos = Point{col, WalkRow}
	if err := s.Apply(SideAttackerA, Action{Kind: ActRamp, UnitID: tower.ID}); err != nil {
		t.Fatalf("второй трап: %v", err)
	}
	if s.unitByID(king.ID) == nil || s.Phase != PhasePlay {
		t.Fatal("короля трапом не достать")
	}
}

// Из башни можно вылезти и без трапа — но только на землю рядом.
func TestCrewClimbsOutWithoutRamp(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	at := openArea(t, s, 2)
	tower := s.spawn(SideAttackerA, UnitSiegeTower, at)
	rider := s.spawn(SideAttackerA, UnitInfantry, at)
	rider.Carrier = tower.ID
	rider.movedTurn, rider.actedTurn = 0, 0

	if tower.Ramp {
		t.Fatal("трап должен быть закрыт")
	}
	spots := s.exitSpots(tower)
	if len(spots) == 0 {
		t.Fatal("рядом с башней должно быть куда вылезти")
	}
	if len(s.unloadSpots(tower)) != 0 {
		t.Fatal("без трапа на стену не сходят")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActUnload, UnitID: rider.ID, To: spots[0]}); err != nil {
		t.Fatalf("выход из башни: %v", err)
	}
	if rider.Carrier != 0 || rider.Pos != spots[0] {
		t.Fatalf("отряд остался в башне: carrier %d, позиция %v", rider.Carrier, rider.Pos)
	}
}

// --- обзор ---

// Обзор срезан всем, а стрелки видят ровно на четыре клетки.
func TestVisionRanges(t *testing.T) {
	for _, tc := range []struct {
		t    UnitType
		want int
	}{
		{UnitInfantry, VisionFoot},
		{UnitGarrison, VisionFoot},
		{UnitKnight, VisionFoot},
		{UnitScout, 4},
		{UnitCrossbow, 4},
		{UnitCatapult, VisionEngine},
	} {
		if got := Spec(tc.t).Vision; got != tc.want {
			t.Fatalf("%s видит на %d, ждали %d", Spec(tc.t).Label, got, tc.want)
		}
	}
	if VisionFoot != 1 || VisionEngine != 1 {
		t.Fatalf("обзор пехоты %d и машин %d — ждали по единице", VisionFoot, VisionEngine)
	}
}

// Вязанки сбрасывают с берега: в самой воде не стоят, и своей клетки в
// списке больше нет — в неё просто не войти.
func TestFascineThrownFromBank(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	at := Point{WallFirstCol + 8, MoatRow}
	u := s.spawn(SideAttackerA, UnitInfantry, at.Add(0, 1))
	u.movedTurn, u.actedTurn = 0, 0
	if !containsPoint(s.fascineSpots(u), at) {
		t.Fatal("соседняя клетка рва должна быть в списке")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: u.ID, Build: "fascine", To: at}); err != nil {
		t.Fatalf("фашины с берега: %v", err)
	}
	if s.moatOpen(at) {
		t.Fatal("клетка должна быть засыпана")
	}

	// Отряд, оказавшийся в воде после разбитого настила, под себя не кидает:
	// вязанку сбрасывают с твёрдой земли.
	stuck := s.spawn(SideAttackerA, UnitInfantry, Point{WallFirstCol + 10, MoatRow})
	stuck.movedTurn, stuck.actedTurn = 0, 0
	if containsPoint(s.fascineSpots(stuck), stuck.Pos) {
		t.Fatal("под себя вязанки не кидают")
	}
}

// Камень катапульты бьёт по людям вдвое сильнее, чем крошит кладку: отряд
// получает полный урон, стена, ворота и постройка — половину.
func TestCatapultIsHalfAgainstMasonry(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := WallFirstCol + 4
	cat := s.spawn(SideAttackerA, UnitCatapult, Point{col, MoatRow + 3})
	cat.movedTurn, cat.actedTurn = 0, 0

	full := Spec(UnitCatapult).Damage
	if got := s.unitDamage(cat); got != full {
		t.Fatalf("по людям катапульта бьёт на %d, ждали %d", got, full)
	}
	if got, want := s.wallDamage(cat), full/2; got != want {
		t.Fatalf("по кладке катапульта бьёт на %d, ждали %d", got, want)
	}

	// И в деле: прямое попадание снимает с сегмента ровно половину.
	before := s.segmentCol(col).HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: cat.ID, To: Point{col, WallRow}}); err != nil {
		t.Fatalf("обстрел стены: %v", err)
	}
	if got, want := before-s.segmentCol(col).HP, full/2; got != want {
		t.Fatalf("сегмент потерял %d прочности, ждали %d", got, want)
	}

	// Звёзды и воодушевление считаются от осадного урона, а не от боевого.
	cat.Kills = 1
	if got, want := s.wallDamage(cat), full/2+VeteranDamage; got != want {
		t.Fatalf("ветеран снимает %d, ждали %d", got, want)
	}
}

// Весь путь целиком: таран доезжает до створки по мосту, а когда мост сбит —
// только по фашинам, которые надо сперва накидать.
func TestRamReachesGateOverBridgeThenFascines(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	col := GateColLeft
	ditch := Point{col, MoatRow}
	gate := Point{col, WallRow}

	bridge := s.buildingAt(ditch)
	if bridge == nil || bridge.Kind != BuildBridge {
		t.Fatal("напротив ворот должен стоять мост")
	}
	ram := s.spawn(SideAttackerA, UnitRam, Point{col, MoatRow + 1})
	ram.movedTurn, ram.actedTurn = 0, 0
	if !containsPoint(s.Reachable(ram), ditch) {
		t.Fatal("по мосту таран обязан пройти")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: ram.ID, To: ditch}); err != nil {
		t.Fatalf("таран на мост: %v", err)
	}
	hp := s.segmentCol(col).HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActAttack, UnitID: ram.ID, To: gate}); err != nil {
		t.Fatalf("удар тарана по воротам: %v", err)
	}
	if s.segmentCol(col).HP >= hp {
		t.Fatalf("ворота целы: было %d, стало %d", hp, s.segmentCol(col).HP)
	}

	// Замок сбивает мост — и вода возвращается вместе с тараном в ней.
	giveTurn(t, s, SideDefender)
	s.damageBuilding(SideDefender, bridge, BridgeHP)
	if s.buildingAt(ditch) != nil {
		t.Fatal("разбитый мост должен исчезнуть")
	}
	if !s.moatOpen(ditch) {
		t.Fatal("на месте разбитого моста снова открытая вода")
	}

	giveTurn(t, s, SideAttackerA)
	second := s.spawn(SideAttackerA, UnitRam, Point{col + 3, MoatRow + 1})
	second.movedTurn, second.actedTurn = 0, 0
	if containsPoint(s.Reachable(second), Point{col + 3, MoatRow}) {
		t.Fatal("в воду таран не идёт")
	}
	foot := s.spawn(SideAttackerA, UnitInfantry, Point{col + 4, MoatRow + 1})
	foot.movedTurn, foot.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: foot.ID, Build: "fascine", To: Point{col + 3, MoatRow}}); err != nil {
		t.Fatalf("фашины в ров: %v", err)
	}
	nextTurn(t, s, SideAttackerA)
	if !containsPoint(s.Reachable(second), Point{col + 3, MoatRow}) {
		t.Fatal("по настилу таран обязан пройти")
	}
}

// Мост у ворот: ничей, стоит с первого дня, замок его ломает, а чинят только
// полевые инженеры осады.
func TestBridgeAtTheGate(t *testing.T) {
	s := newTestGame()
	for _, col := range []int{GateColLeft, GateColRight} {
		at := Point{col, MoatRow}
		b := s.buildingAt(at)
		if b == nil || b.Kind != BuildBridge || b.Owner != SideNeutral || b.MaxHP != BridgeHP {
			t.Fatalf("в столбце %d должен стоять ничей мост на %d прочности", col, BridgeHP)
		}
		if s.moatOpen(at) {
			t.Fatalf("под мостом в столбце %d воды нет", col)
		}
	}

	at := Point{GateColLeft, MoatRow}
	bridge := s.buildingAt(at)

	// Пехота мост не чинит: это работа полевых инженеров осады.
	giveTurn(t, s, SideAttackerA)
	bridge.HP = BridgeHP - EngineerRepair*2
	foot := s.spawn(SideAttackerA, UnitInfantry, Point{GateColLeft, MoatRow + 1})
	foot.movedTurn, foot.actedTurn = 0, 0
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: foot.ID, Build: "repair", To: at}); err == nil {
		t.Fatal("пехота мост чинить не умеет")
	}
	sap := s.spawn(SideAttackerA, UnitSapper, Point{GateColRight, MoatRow + 1})
	sap.movedTurn, sap.actedTurn = 0, 0
	was := bridge.HP
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: sap.ID, Build: "repair", To: at}); err != nil {
		t.Fatalf("полевые инженеры чинят мост: %v", err)
	}
	if got, want := bridge.HP, minInt(was+EngineerRepair, BridgeHP); got != want {
		t.Fatalf("после ремонта у моста %d прочности, ждали %d", got, want)
	}

	// Замку мост — законная цель: он ничей, и ломают его ради воды под стеной.
	giveTurn(t, s, SideDefender)
	eng := s.spawn(SideDefender, UnitEngineer, Point{GateColLeft, MoatRow + 1})
	eng.movedTurn, eng.actedTurn = 0, 0
	was = bridge.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: eng.ID, To: at}); err != nil {
		t.Fatalf("замок бьёт мост: %v", err)
	}
	if bridge.HP >= was {
		t.Fatalf("мост цел после удара: было %d, стало %d", was, bridge.HP)
	}
}

// Открытая решётка пускает штурмующего внутрь — за это вылазка и платит.
func TestOpenGateLetsAttackerIntoTheCastle(t *testing.T) {
	s := newTestGame()
	col := GateColLeft
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "gate", Col: col}); err != nil {
		t.Fatalf("поднять решётку: %v", err)
	}
	giveTurn(t, s, SideAttackerA)
	s.fillMoatAt(Point{col, MoatRow}, SideAttackerA)
	// Расчищаем боевой ход: смотрим геометрию прохода, а не бой в проёме.
	for _, u := range append([]*Unit{}, s.Units...) {
		if u.Owner == SideDefender && u.Pos.Y >= WallRow {
			s.removeUnit(u)
		}
	}
	raider := s.spawn(SideAttackerA, UnitInfantry, Point{col, MoatRow})
	raider.movedTurn, raider.actedTurn = 0, 0
	if !containsPoint(s.Reachable(raider), Point{col, WalkRow}) {
		t.Fatal("через поднятую решётку осаждающий должен проходить на боевой ход")
	}

	// Опустили — и проход закрылся для всех, включая самого защитника.
	giveTurn(t, s, SideDefender)
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "gate", Col: col}); err != nil {
		t.Fatalf("опустить решётку: %v", err)
	}
	giveTurn(t, s, SideAttackerA)
	raider.movedTurn, raider.actedTurn = 0, 0
	if containsPoint(s.Reachable(raider), Point{col, WallRow}) {
		t.Fatal("опущенная решётка не пропускает никого")
	}
}

// --- настил из вязанок ---

// Настил — не состояние клетки, а постройка: его можно разбить, и тогда
// вода возвращается, а машина через неё снова не пройдёт.
func TestFascineDeckCanBeDestroyed(t *testing.T) {
	s := newTestGame()
	col := WallFirstCol + 5
	at := Point{col, MoatRow}
	if !s.fillMoatAt(at, SideAttackerA) {
		t.Fatal("ров должен был засыпаться")
	}
	deck := s.buildingAt(at)
	if deck == nil || deck.Kind != BuildFascine || deck.HP != FascineHP {
		t.Fatalf("настила нет или он не тот: %+v", deck)
	}
	// По настилу ходят все, включая колёсные машины, и без потери темпа.
	if s.moatOpen(at) || s.moveCost(at) != 1 {
		t.Fatalf("по настилу идут как по полю: open=%v cost=%d", s.moatOpen(at), s.moveCost(at))
	}
	giveTurn(t, s, SideAttackerA)
	ram := s.spawn(SideAttackerA, UnitRam, Point{col, MoatRow + 1})
	ram.movedTurn, ram.actedTurn = 0, 0
	if !containsPoint(s.Reachable(ram), at) {
		t.Fatal("таран обязан пройти по настилу")
	}

	// Защитник разбивает переправу — вода возвращается.
	giveTurn(t, s, SideDefender)
	s.damageBuilding(SideDefender, deck, FascineHP)
	if s.buildingAt(at) != nil {
		t.Fatal("разбитый настил должен исчезнуть")
	}
	if !s.moatOpen(at) {
		t.Fatal("после разбитого настила во рву снова вода")
	}
	giveTurn(t, s, SideAttackerA)
	ram.movedTurn, ram.actedTurn = 0, 0
	if containsPoint(s.Reachable(ram), at) {
		t.Fatal("в воду таран не идёт")
	}
}

// Настил — законная цель для защитника, а не часть рельефа.
func TestDefenderCanShootFascines(t *testing.T) {
	s := newTestGame()
	col := WallFirstCol + 5
	at := Point{col, MoatRow}
	s.fillMoatAt(at, SideAttackerA)
	giveTurn(t, s, SideDefender)
	shooter := s.spawn(SideDefender, UnitScout, Point{col, WalkRow})
	shooter.movedTurn, shooter.actedTurn = 0, 0
	if !containsPoint(s.AttackTargets(shooter), at) {
		t.Fatal("настил должен быть в списке целей защитника")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: shooter.ID, To: at}); err != nil {
		t.Fatalf("выстрел по настилу: %v", err)
	}
	deck := s.buildingAt(at)
	if deck == nil || deck.HP >= FascineHP {
		t.Fatalf("настил не пострадал: %+v", deck)
	}
}

// --- полевые инженеры осады ---

// Сапёр ставит частокол и вышку, но в замковые умения не лезет.
func TestSapperBuildsPalisadeAndWatchPost(t *testing.T) {
	spec := Spec(UnitSapper)
	if spec.MaxHP != 14 || spec.Move != 3 || !spec.Builder {
		t.Fatalf("полевой инженер осады: %d HP, шаг %d, builder %v", spec.MaxHP, spec.Move, spec.Builder)
	}
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	at := openArea(t, s, 2)
	s.supplies[SideAttackerA] = 100
	sap := s.spawn(SideAttackerA, UnitSapper, at)
	sap.movedTurn, sap.actedTurn = 0, 0

	spot := at.Add(1, 0)
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: sap.ID, Build: "palisade", To: spot}); err != nil {
		t.Fatalf("частокол: %v", err)
	}
	b := s.buildingAt(spot)
	if b == nil || b.Kind != BuildPalisade || b.Owner != SideAttackerA || b.HP != PalisadeHP {
		t.Fatalf("частокола нет или он не тот: %+v", b)
	}
	if s.passable(SideAttackerA, spot) {
		t.Fatal("частокол перекрывает клетку")
	}

	// Вышку осаде теперь ставит и он, а не только пехота.
	sap.actedTurn = 0
	if !s.canWatchPost(sap) {
		t.Fatal("полевой инженер должен уметь ставить наблюдательный пункт")
	}
	// А вот замковые умения ему недоступны.
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: sap.ID, Build: "trap", To: at.Add(0, 1)}); err == nil {
		t.Fatal("ловушки ставит замок, а не осада")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: sap.ID, Build: "repair", To: spot}); err == nil {
		t.Fatal("чинить осаде нечем")
	}
	// И наоборот: инженеры замка частокол не ставят.
	giveTurn(t, s, SideDefender)
	eng := s.spawn(SideDefender, UnitEngineer, Point{GateColLeft, WalkRow + 0})
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "palisade",
		To: Point{GateColLeft, MoatRow}}); err == nil {
		t.Fatal("частокол — работа осады")
	}
}

// --- мантелет ---

// Щит принимает стрелу вместо того, кто стоит прямо за ним.
func TestMantletTakesTheArrowForTheUnitBehind(t *testing.T) {
	s := newTestGame()
	giveTurn(t, s, SideAttackerA)
	at := openArea(t, s, 3)
	s.supplies[SideAttackerA] = 100
	sap := s.spawn(SideAttackerA, UnitSapper, at)
	sap.movedTurn, sap.actedTurn = 0, 0

	// Стрелок севернее: щит ставим между ним и укрытым отрядом.
	shieldPos := at.Add(0, -1)
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: sap.ID, Build: "mantlet", To: shieldPos}); err != nil {
		t.Fatalf("мантелет: %v", err)
	}
	shield := s.unitAt(shieldPos)
	if shield == nil || shield.Type != UnitMantlet || shield.HP != Spec(UnitMantlet).MaxHP {
		t.Fatalf("щита нет или он не тот: %+v", shield)
	}
	if got := s.Supplies(SideAttackerA); got != 100-MantletCost {
		t.Fatalf("щит обошёлся в %d припасов, ждали %d", 100-got, MantletCost)
	}

	giveTurn(t, s, SideDefender)
	shooter := s.spawn(SideDefender, UnitCrossbow, at.Add(0, -3))
	shooter.movedTurn, shooter.actedTurn = 0, 0
	hpSap, hpShield := sap.HP, shield.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: shooter.ID, To: sap.Pos}); err != nil {
		t.Fatalf("выстрел по укрытому отряду: %v", err)
	}
	if sap.HP != hpSap {
		t.Fatalf("за щитом отряд не должен страдать: было %d, стало %d", hpSap, sap.HP)
	}
	if shield.HP >= hpShield {
		t.Fatalf("щит должен был принять удар: %d -> %d", hpShield, shield.HP)
	}

	// Сбоку щит не помогает: он стоит не на линии.
	nextTurn(t, s, SideDefender)
	flank := s.spawn(SideDefender, UnitCrossbow, at.Add(3, 0))
	flank.movedTurn, flank.actedTurn = 0, 0
	hpSap = sap.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: flank.ID, To: sap.Pos}); err != nil {
		t.Fatalf("выстрел с фланга: %v", err)
	}
	if sap.HP >= hpSap {
		t.Fatal("из-за фланга щит не прикрывает")
	}
}

// В рукопашной щит обходят, а навесом бьют поверх него.
func TestMantletDoesNotStopMeleeOrSplash(t *testing.T) {
	s := newTestGame()
	at := openArea(t, s, 3)
	hidden := s.spawn(SideAttackerA, UnitInfantry, at)
	s.spawn(SideAttackerA, UnitMantlet, at.Add(0, -1))

	giveTurn(t, s, SideDefender)
	// Рукопашная вплотную: щит стоит на соседней клетке, но меч не летит.
	melee := s.spawn(SideDefender, UnitGarrison, at.Add(1, 0))
	melee.movedTurn, melee.actedTurn = 0, 0
	hp := hidden.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: melee.ID, To: at}); err != nil {
		t.Fatalf("рукопашная: %v", err)
	}
	if hidden.HP >= hp {
		t.Fatal("в рукопашной щит не спасает")
	}

	// Навес: камень падает сверху, доска на раме от него не закрывает.
	nextTurn(t, s, SideDefender)
	treb := s.spawn(SideDefender, UnitTrebuchet, at.Add(0, -5))
	treb.movedTurn, treb.actedTurn = 0, 0
	hp = hidden.HP
	if err := s.Apply(SideDefender, Action{Kind: ActAttack, UnitID: treb.ID, To: at}); err != nil {
		t.Fatalf("навесной удар: %v", err)
	}
	if hidden.HP >= hp {
		t.Fatal("от навеса мантелет не спасает")
	}
}

// Прогноз дохода в панели обязан совпадать с тем, что реально приходит в
// начале хода: иначе «+8 в ход» — обещание, которое игра не держит.
func TestIncomeForecastMatchesTurnStart(t *testing.T) {
	s := newTestGame()
	var mine Point
	for _, p := range MineTiles() {
		if p.Y > WallRow {
			mine = p
			break
		}
	}
	s.spawn(SideAttackerA, UnitInfantry, FarmTiles()[0])
	s.spawn(SideAttackerA, UnitInfantry, mine)

	for _, side := range []Side{SideDefender, SideAttackerA} {
		giveTurn(t, s, side)
		you := s.ViewFor(s.SeatOf(side)).You
		if you.Income != IncomeTotal(you.IncomeParts) {
			t.Fatalf("%s: итог %d не равен сумме статей %v", side, you.Income, you.IncomeParts)
		}
		supplies, silver := s.Supplies(side), s.Silver(side)
		s.AP = 0 // остаток очков тоже уходит в припасы — исключаем его
		nextTurn(t, s, side)
		if got := s.Supplies(side) - supplies; got != you.Income {
			t.Fatalf("%s: обещали +%d припасов (%v), пришло +%d", side, you.Income, you.IncomeParts, got)
		}
		if got := s.Silver(side) - silver; got != you.SilverIncome {
			t.Fatalf("%s: обещали +%d серебра, пришло +%d", side, you.SilverIncome, got)
		}
	}

	// Статьи называют источник: игрок должен видеть, что кормят его казармы
	// и пашня, а не просто число.
	has := func(side Side, label string) bool {
		for _, p := range s.ViewFor(s.SeatOf(side)).You.IncomeParts {
			if p.Label == label && p.Amount > 0 {
				return true
			}
		}
		return false
	}
	if !has(SideDefender, "казармы") || !has(SideAttackerA, "пашни") {
		t.Fatal("в статьях дохода нет казарм замка или пашни осаждающего")
	}
	if s.ViewFor(s.SeatOf(SideAttackerA)).You.SilverIncome != SilverPerTile {
		t.Fatal("отряд на руднике должен приносить серебро в прогнозе")
	}
}

// Лавка объясняет, почему позиция недоступна. Порядок важен: если наём на
// день исчерпан, «не хватает припасов» — неправда о том, что мешает.
func TestBuyViewExplainsWhyBlocked(t *testing.T) {
	s := newTestGame()
	side := SideDefender
	why := func() map[string]string {
		out := map[string]string{}
		for _, b := range s.ViewFor(s.SeatOf(side)).Buyable {
			if b.Affordable != (b.Why == "") {
				t.Fatalf("%s: affordable=%v расходится с причиной %q", b.Unit, b.Affordable, b.Why)
			}
			if b.AP != DeployAPCost {
				t.Fatalf("%s: наём стоит %d ОД, а в снимке %d", b.Unit, DeployAPCost, b.AP)
			}
			out[b.Unit] = b.Why
		}
		return out
	}

	s.supplies[side], s.silver[side] = Spec(UnitScout).Cost, 0
	if got := why(); got["scout"] != "" || got["ballista"] != WhySupplies || got["crossbow"] != WhySilver {
		t.Fatalf("разведчик по карману, баллисте не хватает припасов, арбалетчикам — серебра; получили %v", got)
	}
	s.AP = 0
	if got := why(); got["scout"] != WhyAP {
		t.Fatalf("без очков действия причина — ОД, а не %q", got["scout"])
	}
	s.AP = APPerTurn
	for i := 0; i < MaxDeploysPerTurn; i++ {
		s.noteHire(side)
	}
	if got := why(); got["scout"] != WhyHires || got["ballista"] != WhyHires {
		t.Fatalf("исчерпанный наём важнее нехватки припасов: %v", got)
	}

	// Двор: на казармы не хватает припасов — это и есть причина.
	for _, o := range s.ViewFor(s.SeatOf(side)).You.Builds {
		if o.AP != ConstructAPCost {
			t.Fatalf("%s: стройка стоит %d ОД, а в снимке %d", o.Key, ConstructAPCost, o.AP)
		}
		if o.Key == "barracks" && o.Why != WhySupplies {
			t.Fatalf("казармам не хватает припасов, а причина %q", o.Why)
		}
	}
}

// Ополчение: панель различает «уже созывали» и «не хватает припасов» —
// первое ждёт следующего хода, второе лечится доходом.
func TestMilitiaExplainsWhyBlocked(t *testing.T) {
	s := newTestGame()
	militiaWhy := func() string { return s.ViewFor(s.SeatOf(SideDefender)).You.MilitiaWhy }

	s.supplies[SideDefender] = MilitiaCost - 1
	if got := militiaWhy(); got != WhySupplies {
		t.Fatalf("без припасов причина %q, ждали %q", got, WhySupplies)
	}
	s.supplies[SideDefender] = MilitiaCost * 2
	if got := militiaWhy(); got != "" || !s.MilitiaReady() {
		t.Fatalf("с припасами ополчение доступно, а причина %q", got)
	}
	if err := s.Apply(SideDefender, Action{Kind: ActAbility, Ability: "militia"}); err != nil {
		t.Fatalf("созыв ополчения: %v", err)
	}
	if got := militiaWhy(); got != WhyUsed {
		t.Fatalf("после созыва причина %q, ждали %q", got, WhyUsed)
	}
	// Чужим и не в свой ход причина не уезжает: это не их кнопка.
	if s.ViewFor(s.SeatOf(SideAttackerA)).You.MilitiaWhy != "" {
		t.Fatal("осаждающему причина про ополчение ни к чему")
	}
}

// Карточка клетки и строка под полем берут описания из меты: у каждой
// клетки карты и у каждой постройки должны быть название и правило.
func TestMetaDescribesEveryTileAndStructure(t *testing.T) {
	m := NewMeta()
	need := map[string]bool{"woodgate": true, "breach": true}
	// Описания нужны клеткам обеих карт: на второй есть река и арка,
	// которых на «Теснине» нет.
	for _, bm := range m.Boards {
		for _, row := range bm.Tiles {
			for _, ch := range row {
				need[string(ch)] = true
			}
		}
	}
	for key := range need {
		info, ok := m.Terrain[key]
		if !ok || info.Label == "" || info.Short == "" || info.Hint == "" {
			t.Fatalf("у клетки %q нет полного описания: %+v", key, info)
		}
	}
	for kind, name := range buildingNames {
		info, ok := m.Structures[name]
		if !ok || info.Label != kind.Label() || info.Short == "" || info.Hint == "" {
			t.Fatalf("у постройки %q нет полного описания: %+v", name, info)
		}
	}
}

// Свой отряд сообщает, ходил ли он и действовал ли в этом ходу: по этому
// панель отряда объясняет, почему подсветки нет. Противнику это не уходит.
func TestOwnUnitReportsMovedAndActed(t *testing.T) {
	s := newTestGame()
	u := s.spawn(SideDefender, UnitGarrison, s.deployTiles(SideDefender)[0])
	view := func(side Side) (UnitView, bool) {
		for _, uv := range s.ViewFor(s.SeatOf(side)).Units {
			if uv.ID == u.ID {
				return uv, true
			}
		}
		return UnitView{}, false
	}
	if uv, _ := view(SideDefender); uv.Moved || uv.Acted {
		t.Fatalf("в начале хода отряд свеж: moved=%v acted=%v", uv.Moved, uv.Acted)
	}
	moves := s.Reachable(u)
	if len(moves) == 0 {
		t.Fatal("гарнизону некуда шагнуть")
	}
	if err := s.Apply(SideDefender, Action{Kind: ActMove, UnitID: u.ID, To: moves[0]}); err != nil {
		t.Fatalf("шаг: %v", err)
	}
	if uv, _ := view(SideDefender); !uv.Moved || uv.Acted {
		t.Fatalf("после шага ждали moved=true acted=false, получили %v/%v", uv.Moved, uv.Acted)
	}
	if uv, seen := view(SideAttackerA); seen && (uv.Moved || uv.Acted) {
		t.Fatal("противник не должен знать, ходил ли чужой отряд")
	}

	// Фашины: пехоте даром, прочим — очко действия.
	giveTurn(t, s, SideAttackerA)
	inf := s.spawn(SideAttackerA, UnitInfantry, Point{0, CampRowFirst})
	sap := s.spawn(SideAttackerA, UnitSapper, Point{1, CampRowFirst})
	for _, uv := range s.ViewFor(s.SeatOf(SideAttackerA)).Units {
		if uv.ID == inf.ID && (!uv.CanFascine || uv.FascineAP != FascineAPFoot) {
			t.Fatalf("пехота: фашины %v за %d ОД, ждали даром", uv.CanFascine, uv.FascineAP)
		}
		if uv.ID == sap.ID && (!uv.CanFascine || uv.FascineAP != FascineAPCost) {
			t.Fatalf("полевые инженеры: фашины %v за %d ОД, ждали %d", uv.CanFascine, uv.FascineAP, FascineAPCost)
		}
	}
}

// Частокол замка ставят пряслом: одно действие, одна цена — три кола в линию
// поперёк направления на выбранную клетку.
func TestCastleFenceGoesUpAsAThreeCellLine(t *testing.T) {
	s := newTestGame()
	spot := openArea(t, s, 3)
	eng := s.spawn(SideDefender, UnitEngineer, spot)
	s.supplies[SideDefender] = 50
	before := s.supplies[SideDefender]
	ap := s.AP

	// Клик по клетке над инженером: линия встаёт поперёк — три клетки в ряд.
	at := spot.Add(0, -1)
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "barricade", To: at}); err != nil {
		t.Fatalf("установка частокола: %v", err)
	}
	for _, p := range []Point{at.Add(-1, 0), at, at.Add(1, 0)} {
		b := s.buildingAt(p)
		if b == nil || b.Kind != BuildBarricade || b.Owner != SideDefender {
			t.Fatalf("на клетке %v ждали кол частокола замка, нашли %v", p, b)
		}
	}
	if n := s.countBuildings(SideDefender, BuildBarricade); n != BarricadeSpan {
		t.Fatalf("прясло должно быть в %d кола, встало %d", BarricadeSpan, n)
	}
	// Цена и очко действия — как за один кол.
	if spent := before - s.supplies[SideDefender]; spent != BarricadeCost {
		t.Fatalf("за прясло списали %d припасов, ждали %d", spent, BarricadeCost)
	}
	if s.AP != ap-1 {
		t.Fatalf("прясло должно стоить одно очко действия, списали %d", ap-s.AP)
	}

	// Снимок несёт готовые прясла: по ним клиент рисует призрак линии под
	// курсором. Без них крайние колья ему неоткуда взять — они в двух
	// клетках от инженера, а buildAt кончается на соседних.
	s4 := newTestGame()
	spot = openArea(t, s4, 3)
	eng4 := s4.spawn(SideDefender, UnitEngineer, spot)
	view := s4.ViewFor(s4.SeatOf(SideDefender))
	var uv *UnitView
	for i := range view.Units {
		if view.Units[i].ID == eng4.ID {
			uv = &view.Units[i]
		}
	}
	if uv == nil {
		t.Fatal("инженер должен быть в своём же снимке")
	}
	if len(uv.FenceAt) != len(uv.BuildAt) {
		t.Fatalf("прясло должно приехать на каждую годную клетку: клеток %d, прясел %d",
			len(uv.BuildAt), len(uv.FenceAt))
	}
	for _, line := range uv.FenceAt {
		if len(line) != BarricadeSpan {
			t.Fatalf("в чистом поле прясло должно быть в %d кола, приехало %d", BarricadeSpan, len(line))
		}
		if !containsPoint(uv.BuildAt, line[0]) {
			t.Fatalf("первой точкой прясла идёт сама клетка, а пришла %v", line[0])
		}
	}

	// Клик по углу — такая же линия, только по диагонали.
	s2 := newTestGame()
	spot = openArea(t, s2, 3)
	eng2 := s2.spawn(SideDefender, UnitEngineer, spot)
	s2.supplies[SideDefender] = 50
	corner := spot.Add(1, -1)
	if err := s2.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng2.ID, Build: "barricade", To: corner}); err != nil {
		t.Fatalf("установка частокола по диагонали: %v", err)
	}
	for _, p := range []Point{corner.Add(-1, -1), corner, corner.Add(1, 1)} {
		if s2.buildingAt(p) == nil {
			t.Fatalf("диагональное прясло: на клетке %v пусто", p)
		}
	}

	// Занятая клетка просто выпадает из линии, а цена не меняется.
	s3 := newTestGame()
	spot = openArea(t, s3, 3)
	eng3 := s3.spawn(SideDefender, UnitEngineer, spot)
	s3.supplies[SideDefender] = 50
	at = spot.Add(0, -1)
	s3.spawn(SideDefender, UnitGarrison, at.Add(1, 0))
	before = s3.supplies[SideDefender]
	if err := s3.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng3.ID, Build: "barricade", To: at}); err != nil {
		t.Fatalf("установка частокола у занятой клетки: %v", err)
	}
	if n := s3.countBuildings(SideDefender, BuildBarricade); n != BarricadeSpan-1 {
		t.Fatalf("занятая клетка должна выпасть: ждали %d кола, встало %d", BarricadeSpan-1, n)
	}
	if spent := before - s3.supplies[SideDefender]; spent != BarricadeCost {
		t.Fatalf("короткое прясло должно стоить столько же: списали %d, ждали %d", spent, BarricadeCost)
	}
}

// Частокол осады остаётся одиночным: прясло — умение двора, а не поля.
func TestSiegePalisadeStaysASingleStake(t *testing.T) {
	s := newTestGame()
	spot := openArea(t, s, 3)
	sap := s.spawn(SideAttackerA, UnitSapper, spot)
	s.supplies[SideAttackerA] = 50
	giveTurn(t, s, SideAttackerA)
	at := spot.Add(0, -1)
	if err := s.Apply(SideAttackerA, Action{Kind: ActBuild, UnitID: sap.ID, Build: "palisade", To: at}); err != nil {
		t.Fatalf("установка частокола осады: %v", err)
	}
	if n := s.countBuildings(SideAttackerA, BuildPalisade); n != 1 {
		t.Fatalf("осада ставит по колу за действие, встало %d", n)
	}
}

// Наткнувшись в тумане на чужой отряд, марш обрывается о него — но отряд
// проходит путь до упора и встаёт вплотную, а не топчется на месте.
func TestReconByFireStopsNextToTheBlocker(t *testing.T) {
	s := newTestGame()
	centre := openArea(t, s, 5)
	// Враг стоит ровно на пределе шага по прямой: до него пехота идёт весь
	// свой запас и натыкается на находку последним шагом.
	move := Spec(UnitInfantry).Move
	if move < 2 {
		t.Fatalf("тесту нужен шаг хотя бы в 2 клетки, у пехоты %d", move)
	}
	target := centre.Add(0, -move)
	hidden := s.spawn(SideDefender, UnitGarrison, target)
	hidden.spotted = [4]bool{}
	hidden.spotted[SideDefender] = true

	walker := s.spawn(SideAttackerA, UnitInfantry, centre)
	giveTurn(t, s, SideAttackerA)
	if s.VisibleUnit(hidden, SideAttackerA) {
		t.Fatal("отряд в тумане не должен быть виден")
	}
	if !containsPoint(s.Reachable(walker), target) {
		t.Fatal("клетка с невидимым отрядом должна выглядеть свободной")
	}
	if err := s.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: walker.ID, To: target}); err != nil {
		t.Fatalf("шаг в туман: %v", err)
	}
	if walker.Pos == target {
		t.Fatal("занятую клетку шагом не отобрать")
	}
	if walker.Pos == centre {
		t.Fatal("отряд не должен оставаться на месте: он доходит до находки вплотную")
	}
	if d := Chebyshev(walker.Pos, target); d != 1 {
		t.Fatalf("отряд должен встать вплотную к находке, а он в %d клетках", d)
	}
	if want := centre.Add(0, -(move - 1)); walker.Pos != want {
		t.Fatalf("отряд должен был пройти путь до упора в %v, а встал в %v", want, walker.Pos)
	}
	if !s.VisibleUnit(hidden, SideAttackerA) {
		t.Fatal("наткнувшись на чужой отряд, его вскрывают")
	}

	// Находка **посреди** пути: раньше отряд проходил сквозь неё и уходил
	// дальше, оставив её невскрытой. Теперь марш обрывается о неё так же,
	// как о находку в самой цели. Стороны коридора зажимаем частоколом,
	// чтобы обойти было нечем и путь был только один.
	s2 := newTestGame()
	centre = openArea(t, s2, 5)
	id := 0
	for dy := -1; dy >= -move; dy-- {
		for _, dx := range []int{-1, 1} {
			id++
			s2.Buildings = append(s2.Buildings, &Building{
				ID: 800 + id, Kind: BuildBarricade, Owner: SideDefender,
				Pos: centre.Add(dx, dy), HP: BarricadeHP, MaxHP: BarricadeHP,
			})
		}
	}
	mid, far := centre.Add(0, -2), centre.Add(0, -3)
	blocker := s2.spawn(SideDefender, UnitGarrison, mid)
	blocker.spotted = [4]bool{}
	blocker.spotted[SideDefender] = true
	walker2 := s2.spawn(SideAttackerA, UnitInfantry, centre)
	giveTurn(t, s2, SideAttackerA)
	if s2.VisibleUnit(blocker, SideAttackerA) {
		t.Fatal("отряд в двух клетках от пехоты виден быть не должен: обзор у неё одна клетка")
	}
	if err := s2.Apply(SideAttackerA, Action{Kind: ActMove, UnitID: walker2.ID, To: far}); err != nil {
		t.Fatalf("шаг в туман через находку: %v", err)
	}
	if want := centre.Add(0, -1); walker2.Pos != want {
		t.Fatalf("марш должен был оборваться в %v, а отряд оказался в %v", want, walker2.Pos)
	}
	if !s2.VisibleUnit(blocker, SideAttackerA) {
		t.Fatal("находку посреди пути тоже вскрывают")
	}
}

// Туман прячет не только отряды, но и постройки: чужой частокол посреди
// поля в снимок не попадает, пока на его клетку никто не смотрит.
func TestFogHidesBuildings(t *testing.T) {
	s := newTestGame()
	far := openArea(t, s, 3)

	seen := func(side Side, at Point) bool {
		for _, b := range s.ViewFor(s.SeatOf(side)).Buildings {
			if b.Pos == at {
				return true
			}
		}
		return false
	}

	// Замок вяжет прясло в чистом поле, глаз осады рядом нет.
	eng := s.spawn(SideDefender, UnitEngineer, far)
	s.supplies[SideDefender] = 50
	at := far.Add(0, 1)
	if err := s.Apply(SideDefender, Action{Kind: ActBuild, UnitID: eng.ID, Build: "barricade", To: at}); err != nil {
		t.Fatalf("установка частокола: %v", err)
	}
	if !seen(SideDefender, at) {
		t.Fatal("свою постройку хозяин видит всегда")
	}
	if seen(SideAttackerA, at) || seen(SideAttackerB, at) {
		t.Fatal("чужой частокол в тумане не должен попадать в снимок")
	}

	// Подвели разведчика — частокол виден, и сразу обоим осаждающим:
	// разведку они делят.
	s.spawn(SideAttackerA, UnitScout, at.Add(2, 0))
	if !seen(SideAttackerA, at) {
		t.Fatal("под разведчиком постройка должна быть видна")
	}
	if !seen(SideAttackerB, at) {
		t.Fatal("осаждающие делят разведку — союзник тоже должен видеть постройку")
	}

	// Ничейный мост у ворот — часть карты с первого дня, а не постройка
	// стороны: его видно всем и без разведки.
	bridge := Point{GateColLeft, MoatRow}
	if s.buildingAt(bridge) == nil {
		t.Fatalf("на клетке %v должен стоять мост", bridge)
	}
	for _, side := range []Side{SideDefender, SideAttackerA, SideAttackerB} {
		if !seen(side, bridge) {
			t.Fatalf("мост должен быть виден стороне %s в любом случае", side)
		}
	}
}

// Настил, которого не видно, не закрывает воду в снимке: иначе чужие фашины
// читались бы по цвету клетки сквозь туман.
func TestFogHidesFascinesFromTheMoatList(t *testing.T) {
	s := newTestGame()
	// Осада заваливает клетку рва там, куда замок сейчас не смотрит:
	// у ворот своих глаз на воду у него нет.
	at := Point{GateColLeft - 1, MoatRow}
	if TileAt(at) != TileMoat {
		t.Fatalf("клетка %v должна быть рвом, а она %s", at, TileAt(at))
	}
	if s.CanSee(SideDefender, at) {
		t.Fatalf("тесту нужна клетка рва в тумане для замка, а %v просматривается", at)
	}
	if !s.fillMoatAt(at, SideAttackerA) {
		t.Fatal("клетку рва должно было получиться завалить")
	}

	open := func(side Side) bool {
		return containsPoint(s.ViewFor(s.SeatOf(side)).Moat, at)
	}
	if open(SideAttackerA) || open(SideAttackerB) {
		t.Fatal("свой настил хозяин видит — для него клетка уже не открытая вода")
	}
	if !open(SideDefender) {
		t.Fatal("чужой настил в тумане не должен закрывать воду в снимке замка")
	}

	// Замок подвёл глаза — настил виден, и вода в снимке закрылась.
	s.spawn(SideDefender, UnitScout, at.Add(1, 1))
	if open(SideDefender) {
		t.Fatal("увиденный настил должен закрыть воду и в снимке замка")
	}
}
