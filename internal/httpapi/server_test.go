package httpapi

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"siege/internal/config"
	"siege/internal/game"
	"siege/internal/room"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		Version:    "test",
		LogFormat:  "text",
		IceServers: `[{"urls":"stun:stun.l.google.com:19302"}]`,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := room.NewManager(room.Options{MaxRooms: 10, IdleTTL: time.Minute}, log)
	srv := httptest.NewServer(New(cfg, log, mgr, emptyFS{}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// emptyFS подменяет фронтенд: тестам нужен только API.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) { return nil, fs.ErrNotExist }

func post(t *testing.T, srv *httptest.Server, path string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("маршалинг тела: %v", err)
		}
		r = strings.NewReader(string(b))
	}
	res, err := http.Post(srv.URL+path, "application/json", r)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode < 300 {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("разбор ответа %s: %v", path, err)
		}
	}
	return res.StatusCode
}

func TestHotseatRoomFlow(t *testing.T) {
	srv := newTestServer(t)

	var created struct {
		Room  string `json:"room"`
		Token string `json:"token"`
		Mode  string `json:"mode"`
	}
	if code := post(t, srv, "/api/rooms", map[string]any{
		"mode": "hotseat", "names": [3]string{"Аня", "Боря", "Витя"},
	}, &created); code != http.StatusCreated {
		t.Fatalf("создание комнаты: код %d", code)
	}
	if created.Room == "" || created.Token == "" {
		t.Fatalf("пустой ответ на создание: %+v", created)
	}

	// Партия на одном экране стартует сразу: ждать двух игроков не нужно.
	snap := state(t, srv, created.Room, created.Token)
	if snap.Game == nil {
		t.Fatal("игра должна начаться сразу в режиме hotseat")
	}
	if snap.Game.Phase != "play" || snap.Game.Day != 1 {
		t.Fatalf("ждали первый день осады, получили %s/%d", snap.Game.Phase, snap.Game.Day)
	}
	if !snap.Game.You.MyTurn {
		t.Fatal("в hotseat ход всегда принадлежит сидящему за экраном")
	}

	// Защитник заканчивает ход — очередь переходит осаждающему.
	if code := post(t, srv, "/api/rooms/"+created.Room+"/action", map[string]any{
		"token": created.Token, "action": map[string]any{"kind": "endTurn"},
	}, nil); code != http.StatusNoContent {
		t.Fatalf("завершение хода: код %d", code)
	}
	snap = state(t, srv, created.Room, created.Token)
	if snap.Game.Turn != "attackerA" {
		t.Fatalf("после защитника ходит attackerA, получили %s", snap.Game.Turn)
	}

	// Наём в чужой зоне должен отбиваться сервером.
	if code := post(t, srv, "/api/rooms/"+created.Room+"/action", map[string]any{
		"token":  created.Token,
		"action": map[string]any{"kind": "deploy", "unit": "infantry", "to": map[string]int{"x": game.Cols - 1, "y": game.CampRowLast}},
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("наём в чужом лагере должен отбиваться, код %d", code)
	}

	if code := post(t, srv, "/api/rooms/"+created.Room+"/action", map[string]any{
		"token":  created.Token,
		"action": map[string]any{"kind": "deploy", "unit": "infantry", "to": map[string]int{"x": 1, "y": game.CampRowLast}},
	}, nil); code != http.StatusNoContent {
		t.Fatalf("наём в своём лагере: код %d", code)
	}
	// Туман войны: осаждающий видит только свой отряд, гарнизон замка от
	// него скрыт — до стены ещё половина карты. Обзор осаждающие делят на
	// двоих, но у союзника на поле пока тоже пусто.
	snap = state(t, srv, created.Room, created.Token)
	if len(snap.Game.Units) != 1 {
		t.Fatalf("осаждающий должен видеть один свой отряд, видит %d", len(snap.Game.Units))
	}
	if snap.Game.Units[0].Type != "infantry" || !snap.Game.Units[0].Mine {
		t.Fatalf("на поле не тот отряд: %+v", snap.Game.Units[0])
	}
	if len(snap.Game.Fog) == 0 {
		t.Fatal("в снимке должна быть маска тумана")
	}
}

func TestOnlineRoomNeedsThreePlayers(t *testing.T) {
	srv := newTestServer(t)

	var a, b, c struct {
		Room  string `json:"room"`
		Token string `json:"token"`
		Seat  int    `json:"seat"`
	}
	post(t, srv, "/api/rooms", map[string]any{"mode": "online", "name": "Аня"}, &a)
	if snap := state(t, srv, a.Room, a.Token); snap.Game != nil {
		t.Fatal("партия не должна начинаться в одиночку")
	}
	post(t, srv, "/api/rooms/"+a.Room+"/join", map[string]any{"name": "Боря"}, &b)
	post(t, srv, "/api/rooms/"+a.Room+"/join", map[string]any{"name": "Витя"}, &c)

	snap := state(t, srv, a.Room, a.Token)
	if snap.Game == nil {
		t.Fatal("с тремя игроками партия должна начаться")
	}
	if b.Seat != 1 || c.Seat != 2 {
		t.Fatalf("места раздались неверно: %d, %d", b.Seat, c.Seat)
	}
	// Четвёртый лишний.
	var dummy struct{}
	if code := post(t, srv, "/api/rooms/"+a.Room+"/join", map[string]any{"name": "Гена"}, &dummy); code != http.StatusConflict {
		t.Fatalf("четвёртого игрока должны отбить, код %d", code)
	}
	// Чужой ход — чужие действия.
	if code := post(t, srv, "/api/rooms/"+a.Room+"/action", map[string]any{
		"token": b.Token, "action": map[string]any{"kind": "endTurn"},
	}, nil); code != http.StatusConflict {
		t.Fatalf("ход не своей стороной должен отбиваться, код %d", code)
	}
}

func TestEventsStreamSendsState(t *testing.T) {
	srv := newTestServer(t)
	var created struct {
		Room  string `json:"room"`
		Token string `json:"token"`
	}
	post(t, srv, "/api/rooms", map[string]any{"mode": "hotseat", "names": [3]string{"А", "Б", "В"}}, &created)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/rooms/"+created.Room+"/events?token="+created.Token, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("подписка на события: %v", err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("ожидали поток событий, получили %q", ct)
	}

	reader := bufio.NewReader(res.Body)
	deadline := time.Now().Add(3 * time.Second)
	var gotEvent, gotData bool
	for time.Now().Before(deadline) && !gotData {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("чтение потока: %v", err)
		}
		if strings.HasPrefix(line, "event: state") {
			gotEvent = true
		}
		if gotEvent && strings.HasPrefix(line, "data: ") {
			var snap room.Snapshot
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snap); err != nil {
				t.Fatalf("разбор события: %v", err)
			}
			if snap.Room != created.Room || snap.Game == nil {
				t.Fatalf("в событии нет состояния партии: %+v", snap)
			}
			gotData = true
		}
	}
	if !gotData {
		t.Fatal("сервер не прислал состояние в поток за 3 секунды")
	}
}

func TestHealthAndMeta(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/healthz", "/readyz", "/version", "/api/meta"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: код %d", path, res.StatusCode)
		}
	}
}

func state(t *testing.T, srv *httptest.Server, roomID, token string) room.Snapshot {
	t.Helper()
	res, err := http.Get(srv.URL + "/api/rooms/" + roomID + "/state?token=" + token)
	if err != nil {
		t.Fatalf("чтение состояния: %v", err)
	}
	defer res.Body.Close()
	var snap room.Snapshot
	if err := json.NewDecoder(res.Body).Decode(&snap); err != nil {
		t.Fatalf("разбор состояния: %v", err)
	}
	return snap
}

// Карта и характеристики приезжают из /api/meta, поэтому после выкладки
// клиент обязан перепроверить кэш — иначе нарисует старое поле.
func TestMetaRevalidates(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Get(srv.URL + "/api/meta")
	if err != nil {
		t.Fatalf("GET /api/meta: %v", err)
	}
	res.Body.Close()
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("meta должна отдавать ETag")
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("meta не должна кэшироваться без перепроверки, получили %q", cc)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/meta", nil)
	req.Header.Set("If-None-Match", etag)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("повторный GET /api/meta: %v", err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotModified {
		t.Fatalf("при совпадении ETag ждали 304, получили %d", res2.StatusCode)
	}
}

func TestChatFlow(t *testing.T) {
	srv := newTestServer(t)
	var a, b, c struct {
		Room  string `json:"room"`
		Token string `json:"token"`
	}
	post(t, srv, "/api/rooms", map[string]any{"mode": "online", "name": "Аня"}, &a)
	post(t, srv, "/api/rooms/"+a.Room+"/join", map[string]any{"name": "Боря"}, &b)
	post(t, srv, "/api/rooms/"+a.Room+"/join", map[string]any{"name": "Витя"}, &c)

	// В тексте намеренно пробелы по краям и управляющий символ.
	dirty := "  ломаю левую башню" + string(rune(7)) + "  "
	if code := post(t, srv, "/api/rooms/"+a.Room+"/chat", map[string]any{
		"token": b.Token, "text": dirty,
	}, nil); code != http.StatusNoContent {
		t.Fatalf("отправка сообщения: код %d", code)
	}
	if code := post(t, srv, "/api/rooms/"+a.Room+"/chat", map[string]any{
		"token": b.Token, "text": "   ",
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("пустое сообщение должно отбиваться, код %d", code)
	}
	if code := post(t, srv, "/api/rooms/"+a.Room+"/chat", map[string]any{
		"token": "нет такого", "text": "привет",
	}, nil); code != http.StatusUnauthorized {
		t.Fatalf("чужой токен должен отбиваться, код %d", code)
	}

	snap := state(t, srv, a.Room, a.Token)
	if len(snap.Chat) != 1 {
		t.Fatalf("в чате %d сообщений, ждали 1", len(snap.Chat))
	}
	msg := snap.Chat[0]
	if msg.Name != "Боря" {
		t.Fatalf("автор сообщения %q, ждали Борю", msg.Name)
	}
	if msg.Text != "ломаю левую башню" {
		t.Fatalf("текст не очищен: %q", msg.Text)
	}
	if msg.Side == "" || msg.Side == "neutral" {
		t.Fatalf("у сообщения должна быть роль автора, получили %q", msg.Side)
	}
}

func TestVoiceSignallingRelay(t *testing.T) {
	srv := newTestServer(t)
	var a, b, c struct {
		Room  string `json:"room"`
		Token string `json:"token"`
		Seat  int    `json:"seat"`
	}
	post(t, srv, "/api/rooms", map[string]any{"mode": "online", "name": "Аня"}, &a)
	post(t, srv, "/api/rooms/"+a.Room+"/join", map[string]any{"name": "Боря"}, &b)
	post(t, srv, "/api/rooms/"+a.Room+"/join", map[string]any{"name": "Витя"}, &c)

	// Боря слушает поток: без подписки сигнал доставить некому.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/rooms/"+a.Room+"/events?token="+b.Token, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("подписка Бори: %v", err)
	}
	defer res.Body.Close()
	reader := bufio.NewReader(res.Body)

	// Аня входит в звонок — это видно всем в снимке.
	if code := post(t, srv, "/api/rooms/"+a.Room+"/voice", map[string]any{
		"token": a.Token, "active": true,
	}, nil); code != http.StatusNoContent {
		t.Fatalf("вход в звонок: код %d", code)
	}
	snap := state(t, srv, a.Room, c.Token)
	if len(snap.Voice) != 1 || snap.Voice[0] != a.Seat {
		t.Fatalf("в звонке должно быть место %d, в снимке %v", a.Seat, snap.Voice)
	}

	// Сервер не разбирает содержимое сигнала — просто доставляет адресату.
	offer := map[string]any{"type": "offer", "sdp": "v=0 ..."}
	if code := post(t, srv, "/api/rooms/"+a.Room+"/signal", map[string]any{
		"token": a.Token, "to": b.Seat, "data": offer,
	}, nil); code != http.StatusNoContent {
		t.Fatalf("передача сигнала: код %d", code)
	}

	// Чужому месту и самому себе сигналить нельзя.
	if code := post(t, srv, "/api/rooms/"+a.Room+"/signal", map[string]any{
		"token": a.Token, "to": 9, "data": offer,
	}, nil); code != http.StatusConflict {
		t.Fatalf("сигнал на несуществующее место должен отбиваться, код %d", code)
	}
	if code := post(t, srv, "/api/rooms/"+a.Room+"/signal", map[string]any{
		"token": a.Token, "to": a.Seat, "data": offer,
	}, nil); code != http.StatusConflict {
		t.Fatalf("сигнал самому себе должен отбиваться, код %d", code)
	}

	deadline := time.Now().Add(3 * time.Second)
	var gotEvent, gotData bool
	for time.Now().Before(deadline) && !gotData {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("чтение потока: %v", err)
		}
		if strings.HasPrefix(line, "event: signal") {
			gotEvent = true
			continue
		}
		if gotEvent && strings.HasPrefix(line, "data: ") {
			var envelope struct {
				From int             `json:"from"`
				Name string          `json:"name"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope); err != nil {
				t.Fatalf("разбор сигнала: %v", err)
			}
			if envelope.From != a.Seat || envelope.Name != "Аня" {
				t.Fatalf("сигнал пришёл не от того: %+v", envelope)
			}
			if !strings.Contains(string(envelope.Data), "offer") {
				t.Fatalf("содержимое сигнала потерялось: %s", envelope.Data)
			}
			gotData = true
		}
	}
	if !gotData {
		t.Fatal("сигнал не доехал до адресата за 3 секунды")
	}

	// Выход из звонка снимает отметку.
	post(t, srv, "/api/rooms/"+a.Room+"/voice", map[string]any{"token": a.Token, "active": false}, nil)
	if snap := state(t, srv, a.Room, c.Token); len(snap.Voice) != 0 {
		t.Fatalf("после выхода звонок должен опустеть, в снимке %v", snap.Voice)
	}
}

func TestMetaCarriesIceServers(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Get(srv.URL + "/api/meta")
	if err != nil {
		t.Fatalf("GET /api/meta: %v", err)
	}
	defer res.Body.Close()
	var meta struct {
		IceServers []map[string]any `json:"iceServers"`
	}
	if err := json.NewDecoder(res.Body).Decode(&meta); err != nil {
		t.Fatalf("разбор meta: %v", err)
	}
	if len(meta.IceServers) == 0 {
		t.Fatal("клиенту нужен список ICE-серверов, иначе звонок не соберётся")
	}
}

// Боты: на общем экране их выбирают при создании, в сетевой комнате
// сажают из лобби. Уровни — в мете, чтобы клиент не держал их у себя.
func TestBotsOverHTTP(t *testing.T) {
	srv := newTestServer(t)

	res, err := http.Get(srv.URL + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		BotLevels []struct{ Key, Label string } `json:"botLevels"`
	}
	json.NewDecoder(res.Body).Decode(&meta)
	res.Body.Close()
	if len(meta.BotLevels) != 3 || meta.BotLevels[0].Key != "easy" {
		t.Fatalf("в мете должны быть три уровня ботов: %+v", meta.BotLevels)
	}

	// Один экран против двух ботов: человек — третье место.
	var solo struct {
		Room  string `json:"room"`
		Token string `json:"token"`
		Seat  int    `json:"seat"`
	}
	if code := post(t, srv, "/api/rooms", map[string]any{
		"mode": "hotseat", "names": [3]string{"", "", "Витя"}, "bots": [3]string{"easy", "hard", ""},
	}, &solo); code != http.StatusCreated {
		t.Fatalf("партия против ботов: код %d", code)
	}
	if solo.Seat != 2 {
		t.Fatalf("человек должен сидеть на третьем месте, сидит на %d", solo.Seat)
	}
	if code := post(t, srv, "/api/rooms", map[string]any{
		"mode": "hotseat", "bots": [3]string{"гений", "", ""},
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("неизвестный уровень бота должен отбиваться, код %d", code)
	}

	// Сетевая комната: создатель сажает бота, потом убирает его.
	var a struct {
		Room  string `json:"room"`
		Token string `json:"token"`
	}
	post(t, srv, "/api/rooms", map[string]any{"mode": "online", "name": "Аня"}, &a)
	path := "/api/rooms/" + a.Room + "/bot"
	if code := post(t, srv, path, map[string]any{"token": a.Token, "op": "add", "level": "normal"}, nil); code != http.StatusNoContent {
		t.Fatalf("бот в лобби: код %d", code)
	}
	snap := state(t, srv, a.Room, a.Token)
	if len(snap.Players) != 2 || snap.Players[1].Bot != "normal" || snap.NeedPlayers != 1 {
		t.Fatalf("в лобби должен сидеть бот: %+v", snap.Players)
	}
	if code := post(t, srv, path, map[string]any{"token": a.Token, "op": "level", "seat": 1, "level": "hard"}, nil); code != http.StatusNoContent {
		t.Fatalf("смена уровня: код %d", code)
	}
	if code := post(t, srv, path, map[string]any{"token": "чужой", "op": "remove", "seat": 1}, nil); code != http.StatusUnauthorized {
		t.Fatalf("чужой токен должен отбиваться, код %d", code)
	}
	if code := post(t, srv, path, map[string]any{"token": a.Token, "op": "remove", "seat": 1}, nil); code != http.StatusNoContent {
		t.Fatalf("бот должен уходить из лобби: код %d", code)
	}
	if code := post(t, srv, path, map[string]any{"token": a.Token, "op": "dance"}, nil); code != http.StatusBadRequest {
		t.Fatalf("неизвестная операция должна отбиваться, код %d", code)
	}
	if snap := state(t, srv, a.Room, a.Token); len(snap.Players) != 1 {
		t.Fatalf("после удаления бота в лобби один человек: %+v", snap.Players)
	}
}
