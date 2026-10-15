package room

import (
	"encoding/json"
	"strings"
	"testing"

	"siege/internal/game"
)

// online3 поднимает комнату на троих: партия стартует сама, когда места заняты.
func online3(t *testing.T) (*Room, []*Player) {
	t.Helper()
	r := newRoom("TEST1", ModeOnline, 0)
	var ps []*Player
	for _, name := range []string{"Аня", "Боря", "Витя"} {
		p, err := r.Join(name)
		if err != nil {
			t.Fatalf("вход %s: %v", name, err)
		}
		ps = append(ps, p)
	}
	if r.state == nil {
		t.Fatal("партия должна стартовать на третьем игроке")
	}
	return r, ps
}

// snapshotFor возвращает разобранный снимок для конкретного токена.
func snapshotFor(t *testing.T, r *Room, token string) Snapshot {
	t.Helper()
	r.mu.Lock()
	raw := r.encodeLocked(token)
	r.mu.Unlock()
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("разбор снимка: %v", err)
	}
	return snap
}

// Осадный канал — сговор двоих против замка. Защитник не должен видеть его
// ни в интерфейсе, ни в сетевом трафике.
func TestSiegeChannelStaysBetweenAttackers(t *testing.T) {
	r, ps := online3(t)
	const secret = "иду на ворота, бери левую башню"

	bySide := map[game.Side]*Player{}
	for _, p := range ps {
		bySide[r.state.SideOf(p.Seat)] = p
	}
	attacker, rival, castle := bySide[game.SideAttackerA], bySide[game.SideAttackerB], bySide[game.SideDefender]

	if err := r.Say(attacker.Token, secret, ChannelSiege); err != nil {
		t.Fatalf("осадный канал: %v", err)
	}
	if err := r.Say(castle.Token, "слышу вас", ChannelSiege); err == nil {
		t.Fatal("защитник не должен писать в осадный канал")
	}
	if err := r.Say(castle.Token, "привет", "кухня"); err == nil {
		t.Fatal("неизвестный канал должен отбиваться")
	}

	for _, c := range []struct {
		who    *Player
		sees   bool
		access bool
	}{
		{attacker, true, true},
		{rival, true, true},
		{castle, false, false},
	} {
		snap := snapshotFor(t, r, c.who.Token)
		found := false
		for _, m := range snap.Chat {
			if m.Text == secret {
				found = true
			}
		}
		if found != c.sees {
			t.Fatalf("%s: видит реплику %v, ждали %v", c.who.Name, found, c.sees)
		}
		if snap.Siege != c.access {
			t.Fatalf("%s: доступ к каналу %v, ждали %v", c.who.Name, snap.Siege, c.access)
		}
		// Самый надёжный способ проверить утечку — поискать текст в сыром JSON.
		r.mu.Lock()
		raw := string(r.encodeLocked(c.who.Token))
		r.mu.Unlock()
		if strings.Contains(raw, "башню") != c.sees {
			t.Fatalf("%s: сговор в сетевом трафике — %v", c.who.Name, !c.sees)
		}
	}

	// Общий канал слышат все, включая замок.
	if err := r.Say(attacker.Token, "всем привет", ChannelAll); err != nil {
		t.Fatalf("общий канал: %v", err)
	}
	snap := snapshotFor(t, r, castle.Token)
	if len(snap.Chat) != 1 || snap.Chat[0].Text != "всем привет" {
		t.Fatalf("замок должен слышать общий канал, получил %+v", snap.Chat)
	}
}

// Из лобби выйти можно совсем: место освобождается для другого игрока.
func TestLeaveFreesSeatBeforeStart(t *testing.T) {
	r := newRoom("TEST2", ModeOnline, 0)
	aня, err := r.Join("Аня")
	if err != nil {
		t.Fatalf("вход: %v", err)
	}
	if _, err := r.Join("Боря"); err != nil {
		t.Fatalf("вход: %v", err)
	}

	freed, err := r.Leave(aня.Token)
	if err != nil {
		t.Fatalf("выход: %v", err)
	}
	if !freed {
		t.Fatal("до начала партии место должно освобождаться")
	}
	r.mu.Lock()
	n, seat0 := len(r.players), r.players[0].Seat
	r.mu.Unlock()
	if n != 1 {
		t.Fatalf("в комнате осталось %d игроков, ждали 1", n)
	}
	if seat0 != 0 {
		t.Fatalf("места должны идти подряд, у оставшегося место %d", seat0)
	}
	// Освободившееся имя снова свободно, и партия стартует на третьем.
	if _, err := r.Join("Аня"); err != nil {
		t.Fatalf("повторный вход под тем же именем: %v", err)
	}
	if _, err := r.Join("Витя"); err != nil {
		t.Fatalf("третий игрок: %v", err)
	}
	r.mu.Lock()
	started := r.state != nil
	r.mu.Unlock()
	if !started {
		t.Fatal("партия должна стартовать, когда места снова заняты")
	}
	// Чужой токен выходом не управляет.
	if _, err := r.Leave("не-токен"); err == nil {
		t.Fatal("выход по неизвестному токену должен отбиваться")
	}
}

// Из начатой партии место не освобождается — иначе игра на троих развалится,
// — но игрок помечается ушедшим, и его поток событий обрывается.
func TestLeaveKeepsSeatAfterStart(t *testing.T) {
	r, ps := online3(t)
	subID, ch, err := r.Subscribe(ps[0].Token)
	if err != nil {
		t.Fatalf("подписка: %v", err)
	}
	freed, err := r.Leave(ps[0].Token)
	if err != nil {
		t.Fatalf("выход: %v", err)
	}
	if freed {
		t.Fatal("из начатой партии место освобождаться не должно")
	}
	// В канале лежат снимки, разосланные до выхода; важно, что за ними
	// идёт закрытие, а не бесконечное ожидание.
	closed := false
	for i := 0; i < 16 && !closed; i++ {
		if _, open := <-ch; !open {
			closed = true
		}
	}
	if !closed {
		t.Fatal("поток событий ушедшего должен обрываться")
	}
	r.Unsubscribe(subID) // не должно паниковать на уже закрытом канале

	r.mu.Lock()
	n, left, online := len(r.players), r.players[0].left, r.players[0].online
	r.mu.Unlock()
	if n != seats {
		t.Fatalf("игроков осталось %d, ждали %d", n, seats)
	}
	if !left || online {
		t.Fatalf("ушедший помечен неверно: left=%v online=%v", left, online)
	}
	// Остальные видят это в снимке.
	for _, p := range snapshotFor(t, r, ps[1].Token).Players {
		if p.Seat == 0 && !p.Left {
			t.Fatal("в снимке соседа ушедший должен быть помечен")
		}
	}
}
