package room

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var (
	ErrNoRoom    = errors.New("комната не найдена")
	ErrTooMany   = errors.New("слишком много открытых комнат, попробуйте позже")
	ErrBadMode   = errors.New("неизвестный режим игры")
	ErrEmptyName = errors.New("нужно указать имя")
)

// Options — настройки менеджера комнат.
type Options struct {
	MaxRooms    int
	IdleTTL     time.Duration // комната без активности удаляется
	TurnTimeout time.Duration // 0 — ходы без таймера
	// BotDelay — пауза между действиями бота, пока кто-то смотрит: без неё
	// бот отыгрывает ход быстрее, чем его можно разглядеть.
	BotDelay time.Duration
}

// Manager хранит комнаты в памяти процесса. Для одной VM этого достаточно;
// при масштабировании на несколько инстансов понадобится внешнее хранилище
// (см. docs/DEPLOY.md).
type Manager struct {
	opts Options
	log  *slog.Logger

	mu    sync.Mutex
	rooms map[string]*Room
}

func NewManager(opts Options, log *slog.Logger) *Manager {
	if opts.MaxRooms <= 0 {
		opts.MaxRooms = 200
	}
	if opts.IdleTTL <= 0 {
		opts.IdleTTL = 2 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{opts: opts, log: log, rooms: map[string]*Room{}}
}

// Create открывает новую комнату.
func (m *Manager) Create(mode Mode) (*Room, error) {
	if mode != ModeOnline && mode != ModeHotseat {
		return nil, ErrBadMode
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.rooms) >= m.opts.MaxRooms {
		return nil, ErrTooMany
	}
	id := m.freeIDLocked()
	r := newRoom(id, mode, m.opts.TurnTimeout)
	r.botDelay, r.log = m.opts.BotDelay, m.log
	m.rooms[id] = r
	m.log.Info("комната создана", "room", id, "mode", string(mode), "rooms", len(m.rooms))
	return r, nil
}

func (m *Manager) Get(id string) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[id]
	if !ok {
		return nil, ErrNoRoom
	}
	return r, nil
}

func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rooms)
}

func (m *Manager) freeIDLocked() string {
	for {
		id := newRoomID()
		if _, taken := m.rooms[id]; !taken {
			return id
		}
	}
}

// Run — смотритель: следит за таймером хода и чистит простаивающие комнаты.
// Выходит по отмене контекста, поэтому его можно запускать из main.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.sweep(now)
		}
	}
}

func (m *Manager) sweep(now time.Time) {
	m.mu.Lock()
	list := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		list = append(list, r)
	}
	m.mu.Unlock()

	for _, r := range list {
		r.TickDeadline(now)
		if r.Idle(now) > m.opts.IdleTTL {
			m.mu.Lock()
			delete(m.rooms, r.ID)
			m.mu.Unlock()
			r.close()
			m.log.Info("комната удалена по простою", "room", r.ID)
		}
	}
}

// Алфавит без похожих друг на друга символов: код диктуют голосом.
const roomAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newRoomID() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		panic("room: нет источника случайности: " + err.Error())
	}
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = roomAlphabet[int(v)%len(roomAlphabet)]
	}
	return string(out)
}
