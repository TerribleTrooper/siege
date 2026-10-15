package room

import (
	"encoding/json"
	"testing"
	"time"

	"siege/internal/bot"
	"siege/internal/game"
)

// waitBots ждёт, пока боты доходят свои ходы: они ходят в своей горутине.
func waitBots(t *testing.T, r *Room) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		busy := r.botBusy
		r.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("боты не доходили свой ход")
}

// turnSeat — чей сейчас ход, под замком комнаты.
func turnSeat(r *Room) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.SeatOf(r.state.Turn)
}

// Человек сажает двух ботов в лобби — партия начинается сама, а после его
// хода боты отыгрывают свои и возвращают ход ему.
func TestBotsFillLobbyAndPlay(t *testing.T) {
	r := newRoom("BOTS1", ModeOnline, 0)
	me, err := r.Join("Аня")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddBot(me.Token, bot.Easy); err != nil {
		t.Fatalf("первый бот: %v", err)
	}
	if r.state != nil {
		t.Fatal("с одним ботом партия начинаться не должна")
	}
	if _, err := r.AddBot(me.Token, "гений"); err == nil {
		t.Fatal("неизвестный уровень должен отбиваться")
	}
	if _, err := r.AddBot(me.Token, bot.Hard); err != nil {
		t.Fatalf("второй бот: %v", err)
	}
	waitBots(t, r)
	r.mu.Lock()
	started := r.state != nil
	names := map[string]bool{}
	for _, p := range r.players {
		names[p.Name] = true
	}
	r.mu.Unlock()
	if !started {
		t.Fatal("с двумя ботами места заняты — партия должна начаться")
	}
	if len(names) != seats {
		t.Fatalf("имена в комнате должны быть разными: %v", names)
	}
	if _, err := r.AddBot(me.Token, bot.Easy); err == nil {
		t.Fatal("в полную комнату бота не посадить")
	}

	// Замок — место 0, то есть человек. Отыгрываем
	// несколько дней: после каждого хода человека боты должны вернуть ход.
	for day := 0; day < 5; day++ {
		if got := turnSeat(r); got != me.Seat {
			t.Fatalf("день %d: ждали хода человека, ходит место %d", day, got)
		}
		if err := r.Act(me.Token, game.Action{Kind: game.ActEndTurn}); err != nil {
			t.Fatalf("завершение хода: %v", err)
		}
		waitBots(t, r)
	}
	r.mu.Lock()
	day, army := r.state.Day, r.state.ArmySize(game.SideAttackerA)+r.state.ArmySize(game.SideAttackerB)
	r.mu.Unlock()
	if day != 6 {
		t.Fatalf("после пяти кругов должен идти шестой день, идёт %d", day)
	}
	if army == 0 {
		t.Fatal("за пять дней боты-осаждающие должны были кого-то нанять")
	}
}

// Пока ходит бот, человек действовать не может, а снимок ему приходит его
// собственный: бот не делится с ним ни туманом, ни засадами.
func TestHumanCannotActOnBotTurn(t *testing.T) {
	r := newRoom("BOTS2", ModeOnline, 0)
	me, _ := r.Join("Аня")
	r.AddBot(me.Token, bot.Normal)
	r.AddBot(me.Token, bot.Normal)
	waitBots(t, r)

	r.mu.Lock()
	r.botBusy = true // придерживаем ботов, чтобы застать их ход
	r.state.Apply(game.SideDefender, game.Action{Kind: game.ActEndTurn})
	r.mu.Unlock()
	if err := r.Act(me.Token, game.Action{Kind: game.ActEndTurn}); err == nil {
		t.Fatal("человек не должен завершать ход за бота")
	}
	var snap Snapshot
	if err := json.Unmarshal(r.Snapshot(me.Token), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.You == nil || snap.You.Seat != me.Seat || snap.Game.You.MyTurn {
		t.Fatalf("человек должен смотреть со своего места и ждать: %+v", snap.You)
	}
	bots := 0
	for _, p := range snap.Players {
		if p.Bot != "" {
			bots++
			if !p.Online {
				t.Fatalf("бот всегда на связи: %+v", p)
			}
		}
	}
	if bots != 2 {
		t.Fatalf("в снимке должно быть два бота, а их %d", bots)
	}
	// Пустой токен — наблюдатель, а не бот: глаз бота он получить не должен.
	var watcher Snapshot
	if err := json.Unmarshal(r.Snapshot(""), &watcher); err != nil {
		t.Fatal(err)
	}
	if watcher.You != nil {
		t.Fatalf("пустой токен не должен находить ничьё место: %+v", watcher.You)
	}
	r.mu.Lock()
	r.botBusy = false
	r.kickLocked()
	r.mu.Unlock()
	waitBots(t, r)
	if got := turnSeat(r); got != me.Seat {
		t.Fatalf("отпущенные боты должны вернуть ход человеку, ходит место %d", got)
	}
}

// На одном экране против двух ботов экран всегда показывает человека — и
// пока ходят боты тоже.
func TestHotseatAgainstBots(t *testing.T) {
	r := newRoom("BOTS3", ModeHotseat, 0)
	me, err := r.JoinHotseat([3]string{"", "", "Витя"}, [3]bot.Level{bot.Easy, bot.Normal, ""})
	if err != nil {
		t.Fatal(err)
	}
	waitBots(t, r)
	r.mu.Lock()
	seat := r.seatForLocked(me)
	r.mu.Unlock()
	if seat != 2 || me.Name != "Витя" {
		t.Fatalf("экран должен смотреть глазами человека с места 2, а смотрит %d (%s)", seat, me.Name)
	}
	// Замок у места 0 (бот), осаждающий A — место 1 (бот),
	// B — место 2: боты отходили, и ход теперь человека.
	if got := turnSeat(r); got != 2 {
		t.Fatalf("после ботов ход должен быть у человека, а у места %d", got)
	}
	for i := 0; i < 3; i++ {
		if err := r.Act(me.Token, game.Action{Kind: game.ActEndTurn}); err != nil {
			t.Fatalf("ход человека: %v", err)
		}
		waitBots(t, r)
	}
	if err := r.Say(me.Token, "привет", ChannelAll); err != nil {
		t.Fatalf("чат на общем экране: %v", err)
	}
	if _, err := r.JoinHotseat([3]string{}, [3]bot.Level{"злой"}); err != nil {
		t.Fatalf("повторный вход возвращает того же игрока: %v", err)
	}
	bad := newRoom("BOTS4", ModeHotseat, 0)
	if _, err := bad.JoinHotseat([3]string{}, [3]bot.Level{"злой"}); err == nil {
		t.Fatal("неизвестный уровень бота должен отбиваться")
	}
}

// Трое ботов на одном экране доигрывают партию сами, экран им не мешает и
// видит всё поле как наблюдатель.
func TestBotsAloneFinishMatch(t *testing.T) {
	r := newRoom("BOTS5", ModeHotseat, 0)
	r.botBusy = true // придержим ботов, пока не подведём осаду к концу
	viewer, err := r.JoinHotseat([3]string{}, [3]bot.Level{bot.Hard, bot.Normal, bot.Easy})
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.state.Day = game.SiegeDays - 1
	r.botBusy = false
	r.kickLocked()
	r.mu.Unlock()
	waitBots(t, r)
	r.mu.Lock()
	phase, seat := r.state.Phase, r.seatForLocked(viewer)
	r.mu.Unlock()
	if phase != game.PhaseOver {
		t.Fatalf("боты должны доиграть партию, фаза %s", phase)
	}
	if seat != -1 {
		t.Fatalf("без людей экран смотрит как наблюдатель, а смотрит место %d", seat)
	}
}

// Боты ходят сами, пока комната открыта, и останавливаются, когда её закрыли.
func TestClosedRoomStopsBots(t *testing.T) {
	r := newRoom("BOTS8", ModeHotseat, 0)
	if _, err := r.JoinHotseat([3]string{}, [3]bot.Level{bot.Hard, bot.Normal, bot.Easy}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000 && turnDay(r) < 3; i++ {
		time.Sleep(time.Millisecond)
	}
	if turnDay(r) < 3 {
		t.Fatal("боты должны ходить сами")
	}
	r.close()
	waitBots(t, r)
	day := turnDay(r)
	time.Sleep(20 * time.Millisecond)
	if turnDay(r) != day {
		t.Fatal("в закрытой комнате боты ходить не должны")
	}
}

func turnDay(r *Room) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.Day
}

// Игрок ушёл из начатой партии — его место можно отдать боту, и осада идёт
// дальше. Место живого игрока так не отнять.
func TestReplaceLeftPlayerWithBot(t *testing.T) {
	r, ps := online3(t)
	sub, _, err := r.Subscribe(ps[2].Token)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Unsubscribe(sub)
	if err := r.ReplaceWithBot(ps[0].Token, ps[2].Seat, bot.Normal); err == nil {
		t.Fatal("место игрока на связи боту не отдают")
	}
	if err := r.ReplaceWithBot(ps[0].Token, ps[0].Seat, bot.Normal); err == nil {
		t.Fatal("своё место боту не отдают — для этого есть выход из партии")
	}
	if _, err := r.Leave(ps[1].Token); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceWithBot(ps[0].Token, ps[1].Seat, bot.Hard); err != nil {
		t.Fatalf("место ушедшего должно уйти боту: %v", err)
	}
	// Защитник — место 0. После его хода ходит место 1, теперь это бот.
	if err := r.Act(ps[0].Token, game.Action{Kind: game.ActEndTurn}); err != nil {
		t.Fatal(err)
	}
	waitBots(t, r)
	if got := turnSeat(r); got != ps[2].Seat {
		t.Fatalf("бот должен был отходить за место 1, ходит место %d", got)
	}
	if err := r.Act(ps[1].Token, game.Action{Kind: game.ActEndTurn}); err == nil {
		t.Fatal("токен ушедшего больше не должен работать")
	}
	if err := r.SetBotLevel(ps[0].Token, ps[1].Seat, bot.Easy); err != nil {
		t.Fatalf("уровень бота меняется и посреди партии: %v", err)
	}
	if err := r.RemoveBot(ps[0].Token, ps[1].Seat); err == nil {
		t.Fatal("из начатой партии бота не убрать")
	}
}

// В лобби бота можно убрать и сменить ему уровень, а места после этого
// снова идут подряд.
func TestLobbyBotControls(t *testing.T) {
	r := newRoom("BOTS6", ModeOnline, 0)
	me, _ := r.Join("Аня")
	b, err := r.AddBot(me.Token, bot.Easy)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetBotLevel(me.Token, b.Seat, bot.Hard); err != nil {
		t.Fatalf("смена уровня: %v", err)
	}
	if err := r.SetBotLevel(me.Token, me.Seat, bot.Hard); err == nil {
		t.Fatal("человеку уровень не назначают")
	}
	if err := r.RemoveBot("чужой", b.Seat); err == nil {
		t.Fatal("чужой токен ботами не управляет")
	}
	if err := r.RemoveBot(me.Token, b.Seat); err != nil {
		t.Fatalf("бот должен уходить из лобби: %v", err)
	}
	friend, err := r.Join("Боря")
	if err != nil {
		t.Fatal(err)
	}
	if friend.Seat != 1 {
		t.Fatalf("освободившееся место должно достаться следующему, а досталось %d", friend.Seat)
	}
	if err := r.ReplaceWithBot(me.Token, friend.Seat, bot.Easy); err == nil {
		t.Fatal("в лобби место не заменяют ботом — его просто добавляют")
	}
}

// Сломанный бот не роняет сервер и не вешает партию: его ход завершается.
func TestBotPanicEndsItsTurn(t *testing.T) {
	r := newRoom("BOTS7", ModeHotseat, 0)
	r.botBusy = true
	me, err := r.JoinHotseat([3]string{"Аня", "", ""}, [3]bot.Level{"", bot.Easy, bot.Easy})
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.state.Apply(game.SideDefender, game.Action{Kind: game.ActEndTurn})
	r.botAtLocked(1).bot.brain = nil // следующий шаг бота упадёт
	r.botBusy = false
	r.kickLocked()
	r.mu.Unlock()
	waitBots(t, r)
	if got := turnSeat(r); got != me.Seat {
		t.Fatalf("после падения бота ход должен вернуться человеку, а он у места %d", got)
	}
}
