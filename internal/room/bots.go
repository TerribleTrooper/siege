package room

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"runtime/debug"
	"time"

	"siege/internal/bot"
	"siege/internal/game"
)

// Боты в комнате. Бот занимает место так же, как человек: у него есть имя
// и место за столом, роли меняются по кругу. Токена у него нет — ходит за
// него сервер, по одному действию, и каждое действие уходит в общий поток
// снимков: люди видят ход бота так же, как ход живого соперника.

var (
	ErrBadLevel   = errors.New("неизвестный уровень бота")
	ErrNotBot     = errors.New("на этом месте не бот")
	ErrSeatBusy   = errors.New("игрок на этом месте ещё в партии — отдать его место боту нельзя")
	ErrStarted    = errors.New("партия уже идёт")
	ErrHotseatBot = errors.New("на общем экране ботов выбирают при создании партии")
)

const (
	// maxBotFails — сколько отбитых сервером действий бот терпит за ход,
	// прежде чем просто завершить его. Бот проверяет себя сам, но
	// зациклиться на одной ошибке партия не должна ни при каком раскладе.
	maxBotFails = 24
	// maxBotSteps — предохранитель от бесконечного хода: действий за ход
	// бывает от силы пара десятков.
	maxBotSteps = 120
)

// botSeat — место, за которое играет бот.
type botSeat struct {
	brain *bot.Bot
	level bot.Level
	turn  [2]int // какой ход считают steps и fails: день и сторона
	steps int    // действий в этом ходу
	fails int    // и сколько из них сервер отбил
}

func newBotSeat(level bot.Level) *botSeat {
	return &botSeat{brain: bot.New(level, time.Now().UnixNano()), level: level}
}

// newBotPlayerLocked — бот с ещё не занятым в комнате именем.
func (r *Room) newBotPlayerLocked(name string, level bot.Level, seat int) *Player {
	if name == "" || r.nameTakenLocked(name) {
		name = bot.Name(r.nameTakenLocked)
	}
	return &Player{Name: name, Seat: seat, bot: newBotSeat(level)}
}

func (r *Room) nameTakenLocked(name string) bool {
	for _, p := range r.players {
		if p.Name == name {
			return true
		}
	}
	return false
}

// botAtLocked — бот, который сидит на этом месте, или nil.
func (r *Room) botAtLocked(seat int) *Player {
	for _, p := range r.players {
		if p.bot != nil && p.Seat == seat {
			return p
		}
	}
	return nil
}

// AddBot сажает бота на свободное место в лобби. Как и с третьим игроком,
// партия начинается сама, когда места заняты.
func (r *Room) AddBot(token string, level bot.Level) (*Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()
	if r.playerByTokenLocked(token) == nil {
		return nil, ErrBadToken
	}
	if r.Mode == ModeHotseat {
		return nil, ErrHotseatBot
	}
	if _, ok := bot.ParseLevel(string(level)); !ok {
		return nil, ErrBadLevel
	}
	if r.state != nil {
		return nil, ErrStarted
	}
	if len(r.players) >= seats {
		return nil, ErrRoomFull
	}
	p := r.newBotPlayerLocked("", level, len(r.players))
	r.players = append(r.players, p)
	r.maybeStartLocked(nil)
	r.broadcastLocked()
	return p, nil
}

// RemoveBot убирает бота из лобби. Из начатой партии бота не убрать: за
// его место тогда играть было бы некому.
func (r *Room) RemoveBot(token string, seat int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()
	if r.playerByTokenLocked(token) == nil {
		return ErrBadToken
	}
	if r.state != nil {
		return ErrStarted
	}
	p := r.botAtLocked(seat)
	if p == nil {
		return ErrNotBot
	}
	r.dropPlayerLocked(p)
	r.broadcastLocked()
	return nil
}

// SetBotLevel меняет уровень бота — и в лобби, и посреди партии: если бот
// оказался слишком силён или слаб, его можно подкрутить, не начиная заново.
func (r *Room) SetBotLevel(token string, seat int, level bot.Level) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()
	if r.playerByTokenLocked(token) == nil {
		return ErrBadToken
	}
	if _, ok := bot.ParseLevel(string(level)); !ok {
		return ErrBadLevel
	}
	p := r.botAtLocked(seat)
	if p == nil {
		return ErrNotBot
	}
	p.bot.level = level
	p.bot.brain.SetLevel(level)
	r.broadcastLocked()
	return nil
}

// ReplaceWithBot отдаёт боту место игрока, который ушёл из партии или
// пропал со связи. Без этого осада на троих вставала бы на его ходе
// навсегда. Место уходит боту насовсем: токен ушедшего больше не действует.
func (r *Room) ReplaceWithBot(token string, seat int, level bot.Level) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = time.Now()
	me := r.playerByTokenLocked(token)
	if me == nil {
		return ErrBadToken
	}
	if _, ok := bot.ParseLevel(string(level)); !ok {
		return ErrBadLevel
	}
	if r.state == nil {
		return ErrNotStarted
	}
	if r.Mode == ModeHotseat {
		return ErrHotseatBot
	}
	var target *Player
	for _, p := range r.players {
		if p.Seat == seat && p.bot == nil {
			target = p
		}
	}
	if target == nil {
		return ErrNoSuchSeat
	}
	if target == me || (!target.left && r.hasSubLocked(target.Token)) {
		return ErrSeatBusy
	}
	r.closeSubsOfLocked(target.Token)
	delete(r.voice, target.Seat)
	target.Token, target.left, target.online = "", false, false
	target.bot = newBotSeat(level)
	r.kickLocked()
	r.broadcastLocked()
	return nil
}

func (r *Room) hasSubLocked(token string) bool {
	for _, s := range r.subs {
		if s.token == token {
			return true
		}
	}
	return false
}

// hotseatSeatLocked — чьими глазами смотрит общий экран. Обычно это тот,
// чей ход. Пока ходит бот — следующий по очереди человек: он уже сел к
// экрану и смотрит, что творит бот. Людей нет вовсе — экран наблюдателя,
// которому видно всё.
func (r *Room) hotseatSeatLocked() int {
	side := r.state.Turn
	for i := 0; i < seats; i++ {
		seat := r.state.SeatOf(side)
		if r.botAtLocked(seat) == nil {
			return seat
		}
		side = game.SideAfter(side)
	}
	return -1
}

// humansLocked — остался ли в партии хоть один человек. Без людей боты не
// ходят: смотреть некому, а партия всё равно уже брошена.
func (r *Room) humansLocked() bool {
	for _, p := range r.players {
		if p.bot == nil && !p.left {
			return true
		}
	}
	return false
}

// botToMoveLocked — бот, чей сейчас ход, или nil.
func (r *Room) botToMoveLocked() *Player {
	if r.closed || r.state == nil || r.state.Phase != game.PhasePlay || !r.humansLocked() {
		return nil
	}
	return r.botAtLocked(r.state.SeatOf(r.state.Turn))
}

// kickLocked будит ботов: если ходит бот, а его ещё никто не ведёт, ход
// начинается в отдельной горутине. Запрос игрока, завершившего ход, при
// этом не ждёт ботов — они ходят сами, по одному действию.
func (r *Room) kickLocked() {
	if r.botBusy || r.botToMoveLocked() == nil {
		return
	}
	r.botBusy = true
	go r.driveBots()
}

func (r *Room) driveBots() {
	for {
		r.mu.Lock()
		p := r.botToMoveLocked()
		if p == nil {
			r.botBusy = false
			r.mu.Unlock()
			return
		}
		pause := r.botStepSafeLocked(p)
		r.mu.Unlock()
		if pause > 0 {
			select {
			case <-time.After(pause):
			case <-r.wake:
			}
		}
	}
}

// botStepSafeLocked — шаг бота под присмотром. Ошибка в боте не должна ни
// ронять сервер со всеми его комнатами, ни вешать партию на ходе бота:
// ход за него просто завершается, а ошибка уходит в журнал.
func (r *Room) botStepSafeLocked(p *Player) (pause time.Duration) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if r.log != nil {
			r.log.Error("бот упал, его ход завершён", "room", r.ID, "seat", p.Seat, "panic", v,
				"stack", string(debug.Stack()))
		}
		if r.state != nil && r.state.Phase == game.PhasePlay {
			_ = r.state.Apply(r.state.Turn, game.Action{Kind: game.ActEndTurn})
			r.resetDeadlineLocked()
		}
		r.broadcastLocked()
		pause = 0
	}()
	return r.botStepLocked(p)
}

// botStepLocked — одно действие бота. Возвращает, сколько подождать перед
// следующим: пауза нужна, только если люди успели что-то увидеть.
func (r *Room) botStepLocked(p *Player) time.Duration {
	side := r.state.Turn
	seat := r.state.SeatOf(side)
	// Ход мог кончиться и без бота — по таймеру: счёт начинаем заново.
	if turn := [2]int{r.state.Day, int(side)}; turn != p.bot.turn {
		p.bot.turn, p.bot.steps, p.bot.fails = turn, 0, 0
	}
	before := r.sceneLocked()

	view := r.state.ViewFor(seat)
	a := p.bot.brain.Next(&view)
	if p.bot.fails >= maxBotFails || p.bot.steps >= maxBotSteps {
		a = game.Action{Kind: game.ActEndTurn}
	}
	if err := r.state.Apply(side, a); err != nil {
		p.bot.brain.Reject(a)
		p.bot.fails++
		return 0
	}
	p.bot.steps++
	if a.Kind == game.ActEndTurn || r.state.Turn != side || r.state.Phase != game.PhasePlay {
		r.resetDeadlineLocked()
	}
	r.broadcastLocked()
	if a.Kind == game.ActEndTurn || r.botDelay <= 0 || r.sceneLocked() == before {
		return 0
	}
	return r.botDelay
}

// sceneLocked — отпечаток того, что сейчас видят люди у экранов: отряды,
// стена, постройки. Ход бота в тумане меняет только очки действия, и ждать
// ради него незачем; а вот шаг на виду человек должен успеть разглядеть.
func (r *Room) sceneLocked() uint64 {
	h := fnv.New64a()
	watched := false
	for _, seat := range r.watchersLocked() {
		v := r.state.ViewFor(seat)
		b, _ := json.Marshal(struct {
			Units     []game.UnitView
			Wall      []game.WallSegment
			Buildings []game.BuildingView
			Moat      []game.Point
		}{v.Units, v.Wall, v.Buildings, v.Moat})
		_, _ = h.Write(b)
		watched = true
	}
	if !watched {
		return 0
	}
	return h.Sum64()
}

// watchersLocked — чьими глазами сейчас смотрят люди, у которых открыт
// поток. На общем экране смотрит один, в сетевой партии — каждый своими.
func (r *Room) watchersLocked() []int {
	var out []int
	seen := map[int]bool{}
	for _, sub := range r.subs {
		p := r.playerByTokenLocked(sub.token)
		if p == nil {
			continue
		}
		if seat := r.seatForLocked(p); !seen[seat] {
			seen[seat] = true
			out = append(out, seat)
		}
	}
	return out
}

// close останавливает комнату: её бот больше не ходит и не ждёт.
func (r *Room) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.wake)
	}
}
