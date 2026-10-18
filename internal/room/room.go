package room

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"siege/internal/bot"
	"siege/internal/game"
)

// Чат: короткий, без истории на диске — он живёт столько же, сколько партия.
const (
	maxChatMessages = 60
	maxChatRunes    = 200
)

var (
	ErrBadSeat      = errors.New("неверное место получателя")
	ErrNoSuchSeat   = errors.New("на этом месте ещё никого нет")
	ErrNotListening = errors.New("собеседник не на связи")
	ErrEmptyMessage = errors.New("пустое сообщение")
	ErrRoomFull     = errors.New("в комнате уже три игрока")
	ErrBadToken     = errors.New("неизвестный токен игрока")
	ErrNotStarted   = errors.New("партия ещё не началась")
	ErrNameTaken    = errors.New("это имя уже занято")
	ErrWrongPlayer  = errors.New("сейчас ход другого игрока")
	ErrBadChannel   = errors.New("неизвестный канал чата")
	ErrNotAttacker  = errors.New("осадный канал — только для осаждающих")
)

// Mode — как сидят игроки.
type Mode string

const (
	// ModeOnline: три клиента, у каждого свой токен и своя скрытая информация.
	ModeOnline Mode = "online"
	// ModeHotseat: один экран на всех, ходы по очереди за одним компьютером.
	ModeHotseat Mode = "hotseat"
)

const seats = 3

type Player struct {
	Token string `json:"-"`
	Name  string `json:"name"`
	Seat  int    `json:"seat"`

	online   bool
	left     bool // вышел сам, а не просто потерял связь
	lastSeen time.Time

	// bot — место занято ботом: за него ходит сервер, токена у него нет.
	bot *botSeat
}

// Каналы чата. Общий слышат все; в «осадном» переговариваются между собой
// двое осаждающих — защитник его не видит. Сговор против замка в правилах
// не записан, но место для него теперь есть.
const (
	ChannelAll   = "all"
	ChannelSiege = "siege"
)

// Message — реплика в чате комнаты.
type Message struct {
	Name    string `json:"name"`
	Side    string `json:"side"` // роль автора: для цвета
	Text    string `json:"text"`
	Ms      int64  `json:"ms"`
	Channel string `json:"channel"`

	// audience — места, которым реплика видна: у осадного канала это оба
	// осаждающих. nil — общий канал, видно всем.
	audience []int
}

// visibleTo — видит ли место seat эту реплику. Наблюдатель (seat < 0)
// читает только общий канал.
func (m Message) visibleTo(seat int) bool {
	if m.audience == nil {
		return true
	}
	for _, s := range m.audience {
		if s == seat {
			return true
		}
	}
	return false
}

// sseMessage — кадр в потоке клиента. Кроме снимков партии по этому же
// каналу ходят сигналы WebRTC: отдельного сокета для звонков не нужно.
type sseMessage struct {
	Event string
	Data  []byte
}

type subscriber struct {
	id    int
	token string
	ch    chan sseMessage
}

// Room — одна партия плюс подписчики на её состояние.
type Room struct {
	ID   string
	Mode Mode

	// board — карта этой комнаты. Выбирается при создании и дальше не
	// меняется: партию на ней начинают все, кто в комнату зашёл.
	board *game.Board

	mu       sync.Mutex
	state    *game.State
	players  []*Player
	chat     []Message
	voice    map[int]bool // места, которые сейчас в голосовом звонке
	subs     map[int]*subscriber
	nextSub  int
	created  time.Time
	touched  time.Time
	deadline time.Time // дедлайн текущего хода, zero — без таймера
	timeout  time.Duration

	// Боты. botDelay — пауза между их действиями, пока кто-то смотрит;
	// botBusy — бот уже ходит в своей горутине; closed и wake — комната
	// закрыта и спящего бота пора разбудить, чтобы он вышел.
	botDelay time.Duration
	botBusy  bool
	closed   bool
	wake     chan struct{}
	log      *slog.Logger // nil — комната молчит (так её поднимают тесты)
}

func newRoom(id string, mode Mode, turnTimeout time.Duration) *Room {
	now := time.Now()
	return &Room{
		ID:      id,
		Mode:    mode,
		board:   game.Classic,
		voice:   map[int]bool{},
		subs:    map[int]*subscriber{},
		created: now,
		touched: now,
		timeout: turnTimeout,
		wake:    make(chan struct{}),
	}
}

// Join добавляет игрока. В hotseat-режиме первый вход занимает все три места.
func (r *Room) Join(name string) (*Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()

	if r.Mode == ModeHotseat && len(r.players) > 0 {
		// Один токен на всех: возвращаем уже существующего игрока.
		return r.players[0], nil
	}
	if len(r.players) >= seats {
		return nil, ErrRoomFull
	}
	for _, p := range r.players {
		if p.Name == name {
			return nil, ErrNameTaken
		}
	}
	p := &Player{Token: newToken(), Name: name, Seat: len(r.players), online: true, lastSeen: time.Now()}
	r.players = append(r.players, p)
	r.maybeStartLocked(nil)
	r.broadcastLocked()
	return p, nil
}

// Leave выпускает игрока из комнаты. Пока партия не началась, место
// освобождается совсем — в лобби можно ошибиться комнатой и выйти. Если
// осада уже идёт, место остаётся за игроком (иначе партия на троих
// рассыплется), но он помечается ушедшим: остальные это видят.
//
// Возвращает true, если место освободилось полностью.
func (r *Room) Leave(token string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()

	p := r.playerByTokenLocked(token)
	if p == nil {
		return false, ErrBadToken
	}
	// Hotseat — один экран на всех: выходит сразу вся партия.
	freed := r.state == nil || r.Mode == ModeHotseat
	if freed {
		r.dropPlayerLocked(p)
		if r.Mode == ModeHotseat && r.state != nil && !r.closed {
			r.closed = true // боты за брошенным экраном не доигрывают
			close(r.wake)
		}
	} else {
		p.online = false
		p.left = true
	}
	delete(r.voice, p.Seat) // ушёл — значит вышел и из звонка
	r.closeSubsOfLocked(token)
	r.broadcastLocked()
	return freed, nil
}

// dropPlayerLocked убирает игрока и перенумеровывает места: места в комнате
// идут подряд, иначе следующий вошедший получит дырку в списке.
func (r *Room) dropPlayerLocked(p *Player) {
	for i, x := range r.players {
		if x == p {
			r.players = append(r.players[:i], r.players[i+1:]...)
			break
		}
	}
	for i, x := range r.players {
		x.Seat = i
	}
}

// closeSubsOfLocked обрывает поток событий ушедшего: его вкладка больше не
// должна получать снимки партии.
func (r *Room) closeSubsOfLocked(token string) {
	for id, sub := range r.subs {
		if sub.token == token {
			close(sub.ch)
			delete(r.subs, id)
		}
	}
}

// JoinHotseat создаёт партию на одном экране сразу с тремя именами. bots —
// уровень бота для каждого места, пустая строка — за местом человек. Так
// играют и в одиночку: себе одно место, двум другим ботов.
func (r *Room) JoinHotseat(names [3]string, bots [3]bot.Level) (*Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.players) > 0 {
		return r.players[0], nil
	}
	for _, l := range bots {
		if _, ok := bot.ParseLevel(string(l)); l != "" && !ok {
			return nil, ErrBadLevel
		}
	}
	// Токен общего экрана — у людей. Его место — первое человеческое, а
	// если людей нет вовсе, экран просто смотрит, как играют боты.
	holder := &Player{Token: newToken(), Name: "Зритель", Seat: -1, online: true, lastSeen: time.Now()}
	for seat := seats - 1; seat >= 0; seat-- {
		if bots[seat] == "" {
			holder.Seat, holder.Name = seat, names[seat]
		}
	}
	r.players = append(r.players, holder)
	// Имя боту — такое, которого нет ни у людей за этим экраном, ни у
	// других ботов: игроков различают по имени — на плашках, в чате, в итогах.
	taken := func(name string) bool {
		for seat, l := range bots {
			if l == "" && names[seat] == name {
				return true
			}
		}
		return r.nameTakenLocked(name)
	}
	for seat, l := range bots {
		if l == "" {
			continue
		}
		name := names[seat]
		if name == "" || taken(name) {
			name = bot.Name(taken)
		}
		names[seat] = name
		r.players = append(r.players, &Player{Name: name, Seat: seat, bot: newBotSeat(l)})
	}
	r.maybeStartLocked(&names)
	r.broadcastLocked()
	return holder, nil
}

func (r *Room) maybeStartLocked(hotseatNames *[3]string) {
	if r.state != nil {
		return
	}
	switch {
	case hotseatNames != nil:
		r.state = game.NewOn(r.board, *hotseatNames)
	case len(r.players) == seats:
		var names [3]string
		for i, p := range r.players {
			names[i] = p.Name
		}
		r.state = game.NewOn(r.board, names)
	default:
		return
	}
	r.resetDeadlineLocked()
	r.kickLocked()
}

func (r *Room) resetDeadlineLocked() {
	if r.timeout <= 0 || r.state == nil || r.state.Phase != game.PhasePlay {
		r.deadline = time.Time{}
		return
	}
	r.deadline = time.Now().Add(r.timeout)
}

func (r *Room) playerByTokenLocked(token string) *Player {
	if token == "" {
		return nil // у ботов токена нет, и пустой токен — это наблюдатель
	}
	for _, p := range r.players {
		if p.bot == nil && p.Token == token {
			return p
		}
	}
	return nil
}

// seatForLocked — за какое место играет этот токен прямо сейчас.
// В hotseat это место того, чей ход (а пока ходит бот — следующего
// человека), в online — собственное место игрока.
func (r *Room) seatForLocked(p *Player) int {
	if r.Mode == ModeHotseat && r.state != nil {
		return r.hotseatSeatLocked()
	}
	return p.Seat
}

// Act применяет действие игрока к партии.
func (r *Room) Act(token string, a game.Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()

	p := r.playerByTokenLocked(token)
	if p == nil {
		return ErrBadToken
	}
	if r.state == nil {
		return ErrNotStarted
	}
	seat := r.seatForLocked(p)
	side := r.state.SideOf(seat)
	if err := r.state.Apply(side, a); err != nil {
		return err
	}
	if a.Kind == game.ActEndTurn || r.state.Phase != game.PhasePlay {
		r.resetDeadlineLocked()
	}
	r.kickLocked()
	r.broadcastLocked()
	return nil
}

// Say добавляет реплику в чат комнаты. channel — ChannelAll или ChannelSiege.
func (r *Room) Say(token, text, channel string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()

	p := r.playerByTokenLocked(token)
	if p == nil {
		return ErrBadToken
	}
	text = cleanMessage(text)
	if text == "" {
		return ErrEmptyMessage
	}

	if channel == "" {
		channel = ChannelAll
	}
	if channel != ChannelAll && channel != ChannelSiege {
		return ErrBadChannel
	}

	msg := Message{Name: p.Name, Text: text, Ms: time.Now().UnixMilli(), Side: "neutral", Channel: channel}
	if r.state != nil {
		seat := r.seatForLocked(p)
		msg.Side = r.state.SideOf(seat).String()
		// В hotseat за экраном сидит тот, чей сейчас ход, — им и подписываем.
		if r.Mode == ModeHotseat && seat >= 0 {
			msg.Name = r.state.Seats[seat].Name
		}
		if channel == ChannelSiege {
			if !r.state.SideOf(seat).IsAttacker() {
				return ErrNotAttacker
			}
			msg.audience = []int{
				r.state.SeatOf(game.SideAttackerA),
				r.state.SeatOf(game.SideAttackerB),
			}
		}
	} else if channel == ChannelSiege {
		return ErrNotStarted
	}
	r.chat = append(r.chat, msg)
	if len(r.chat) > maxChatMessages {
		r.chat = r.chat[len(r.chat)-maxChatMessages:]
	}
	r.broadcastLocked()
	return nil
}

// cleanMessage убирает управляющие символы и подрезает длину: в чат уходит
// то, что видно, и ничего сверх того.
func cleanMessage(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	cleaned = strings.TrimSpace(cleaned)
	if runes := []rune(cleaned); len(runes) > maxChatRunes {
		cleaned = string(runes[:maxChatRunes])
	}
	return cleaned
}

// TickDeadline завершает ход, если истекло время. Вызывается смотрителем.
func (r *Room) TickDeadline(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == nil || r.state.Phase != game.PhasePlay || r.deadline.IsZero() || now.Before(r.deadline) {
		return
	}
	_ = r.state.Apply(r.state.Turn, game.Action{Kind: game.ActEndTurn})
	r.resetDeadlineLocked()
	r.kickLocked()
	r.broadcastLocked()
}

// Idle — сколько комната простаивает без активности.
func (r *Room) Idle(now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.touched)
}

// Subscribe подписывает клиента на поток состояний. Первый снимок уходит сразу.
func (r *Room) Subscribe(token string) (int, <-chan sseMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if token != "" && r.playerByTokenLocked(token) == nil {
		return 0, nil, ErrBadToken
	}
	if p := r.playerByTokenLocked(token); p != nil {
		p.online = true
		p.lastSeen = time.Now()
	}
	r.nextSub++
	// Буфер с запасом: кроме снимков по каналу идут сигналы WebRTC.
	sub := &subscriber{id: r.nextSub, token: token, ch: make(chan sseMessage, 32)}
	r.subs[sub.id] = sub
	r.touched = time.Now()
	sub.ch <- sseMessage{Event: "state", Data: r.encodeLocked(token)}
	r.broadcastLocked() // остальные видят, что игрок подключился
	return sub.id, sub.ch, nil
}

func (r *Room) Unsubscribe(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.subs[id]
	if !ok {
		return
	}
	delete(r.subs, id)
	close(sub.ch)
	if p := r.playerByTokenLocked(sub.token); p != nil && !r.hasOtherSubLocked(sub.token, id) {
		p.online = false
		// Оборвалась вкладка — значит оборвался и звонок с этого места.
		delete(r.voice, p.Seat)
	}
	r.broadcastLocked()
}

func (r *Room) hasOtherSubLocked(token string, except int) bool {
	for id, s := range r.subs {
		if id != except && s.token == token {
			return true
		}
	}
	return false
}

func (r *Room) broadcastLocked() {
	for _, sub := range r.subs {
		select {
		case sub.ch <- sseMessage{Event: "state", Data: r.encodeLocked(sub.token)}:
		default:
			// Медленный клиент: пропускаем кадр, следующий снимок всё равно полный.
		}
	}
}

// --- голосовая связь ---

// SetVoice отмечает, что игрок вошёл в звонок или вышел из него.
// Остальные узнают об этом из снимка и сами начнут соединение.
func (r *Room) SetVoice(token string, on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()

	p := r.playerByTokenLocked(token)
	if p == nil {
		return ErrBadToken
	}
	if on {
		r.voice[p.Seat] = true
	} else {
		delete(r.voice, p.Seat)
	}
	r.broadcastLocked()
	return nil
}

// Signal перекладывает сигнал WebRTC от одного игрока к другому. Содержимое
// для сервера непрозрачно: он не разбирает SDP, только проверяет отправителя
// и доставляет адресату.
func (r *Room) Signal(token string, toSeat int, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()

	from := r.playerByTokenLocked(token)
	if from == nil {
		return ErrBadToken
	}
	if toSeat < 0 || toSeat >= seats || toSeat == from.Seat {
		return ErrBadSeat
	}
	var target *Player
	for _, p := range r.players {
		if p.Seat == toSeat {
			target = p
		}
	}
	if target == nil {
		return ErrNoSuchSeat
	}

	envelope := mustJSON(signalEnvelope{From: from.Seat, Name: from.Name, Data: data})
	delivered := false
	for _, sub := range r.subs {
		if sub.token != target.Token {
			continue
		}
		select {
		case sub.ch <- sseMessage{Event: "signal", Data: envelope}:
			delivered = true
		default:
			// Канал забит: сигнал теряется, звонок переустановится сам.
		}
	}
	if !delivered {
		return ErrNotListening
	}
	return nil
}

type signalEnvelope struct {
	From int             `json:"from"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

// Snapshot отдаёт текущее состояние для токена (для первого запроса по HTTP).
func (r *Room) Snapshot(token string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.encodeLocked(token)
}

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand не должен падать; если упал — лучше шумно, чем тихо.
		panic("room: нет источника случайности: " + err.Error())
	}
	return hex.EncodeToString(b)
}
