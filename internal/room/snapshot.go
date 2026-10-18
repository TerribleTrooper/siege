package room

import (
	"encoding/json"
	"time"

	"siege/internal/game"
)

type PlayerInfo struct {
	Name   string `json:"name"`
	Seat   int    `json:"seat"`
	Online bool   `json:"online"`
	Left   bool   `json:"left,omitempty"` // вышел сам, а не потерял связь
	// Bot — уровень бота, если место занял бот; у людей поле пустое.
	Bot string `json:"bot,omitempty"`
}

type YouInfo struct {
	Seat    int    `json:"seat"`
	Name    string `json:"name"`
	Hotseat bool   `json:"hotseat"`
}

// Snapshot — то, что клиент получает по SSE. Содержит отфильтрованный вид
// партии: скрытую информацию чужой стороны сюда не кладём.
type Snapshot struct {
	Room string `json:"room"`
	Mode Mode   `json:"mode"`
	// Map — ключ карты комнаты. Нужен уже в лобби: игроки должны видеть,
	// на какой карте собираются играть, до того как партия началась.
	Map         string       `json:"map"`
	MapName     string       `json:"mapName"`
	Phase       string       `json:"phase"`
	Players     []PlayerInfo `json:"players"`
	NeedPlayers int          `json:"needPlayers"`
	You         *YouInfo     `json:"you,omitempty"`
	Game        *game.View   `json:"game,omitempty"`
	Chat        []Message    `json:"chat,omitempty"`
	Siege       bool         `json:"siege"` // доступен ли осадный канал чата
	Voice       []int        `json:"voice"` // места, которые сейчас в звонке
	DeadlineMs  int64        `json:"deadlineMs,omitempty"`
	NowMs       int64        `json:"nowMs"`
}

func (r *Room) encodeLocked(token string) []byte {
	snap := Snapshot{
		Room:    r.ID,
		Mode:    r.Mode,
		Map:     r.board.Key,
		MapName: r.board.Name,
		Phase:   "lobby",
		NowMs:   time.Now().UnixMilli(),
		Players: []PlayerInfo{},
		Chat:    []Message{},
	}
	for _, p := range r.players {
		info := PlayerInfo{Name: p.Name, Seat: p.Seat, Online: p.online, Left: p.left}
		if p.bot != nil {
			// Бот на связи всегда: он сидит на сервере.
			info.Bot, info.Online = string(p.bot.level), true
		}
		snap.Players = append(snap.Players, info)
	}

	snap.Voice = []int{}
	for seat := 0; seat < seats; seat++ {
		if r.voice[seat] {
			snap.Voice = append(snap.Voice, seat)
		}
	}

	me := r.playerByTokenLocked(token)
	seat := -1
	if me != nil {
		seat = r.seatForLocked(me)
	}

	// Осадный канал не кладём в чужой снимок: подсмотреть сговор в сетевом
	// трафике должно быть так же нельзя, как и засаду в кустах.
	for _, m := range r.chat {
		if m.visibleTo(seat) {
			snap.Chat = append(snap.Chat, m)
		}
	}
	snap.Siege = r.state != nil && seat >= 0 && r.state.SideOf(seat).IsAttacker()

	if r.state == nil {
		snap.NeedPlayers = seats - len(r.players)
		if me != nil {
			snap.You = &YouInfo{Seat: me.Seat, Name: me.Name, Hotseat: r.Mode == ModeHotseat}
		}
		return mustJSON(snap)
	}

	view := r.state.ViewFor(seat)
	snap.Phase = view.Phase
	snap.Game = &view
	if me != nil {
		name := me.Name
		if r.Mode == ModeHotseat && seat >= 0 {
			name = view.Seats[seat].Name // в hotseat имя берём у того, чей ход
		}
		snap.You = &YouInfo{Seat: seat, Name: name, Hotseat: r.Mode == ModeHotseat}
	}
	if !r.deadline.IsZero() {
		snap.DeadlineMs = r.deadline.UnixMilli()
	}
	return mustJSON(snap)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Структуры фиксированные: ошибка здесь означает баг в типах.
		panic("room: не удалось сериализовать снимок: " + err.Error())
	}
	return b
}
