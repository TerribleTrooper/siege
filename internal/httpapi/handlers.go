package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"siege/internal/bot"
	"siege/internal/game"
	"siege/internal/room"
)

const maxNameLen = 24

type createRoomRequest struct {
	Mode  string    `json:"mode"`  // online | hotseat
	Name  string    `json:"name"`  // online: имя создателя
	Names [3]string `json:"names"` // hotseat: три имени сразу
	// Bots — hotseat: уровень бота для каждого места, пусто — человек.
	Bots [3]string `json:"bots"`
	// Map — ключ карты: classic | split. Пусто — классическая.
	Map string `json:"map"`
}

type joinResponse struct {
	Room  string    `json:"room"`
	Mode  room.Mode `json:"mode"`
	Token string    `json:"token"`
	Seat  int       `json:"seat"`
	Name  string    `json:"name"`
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	var req createRoomRequest
	if !decode(w, r, &req) {
		return
	}
	mode := room.Mode(strings.ToLower(strings.TrimSpace(req.Mode)))
	if mode == "" {
		mode = room.ModeOnline
	}

	rm, err := s.rooms.Create(mode, strings.ToLower(strings.TrimSpace(req.Map)))
	if err != nil {
		writeRoomError(w, err)
		return
	}

	var p *room.Player
	if mode == room.ModeHotseat {
		names := req.Names
		var bots [3]bot.Level
		for i := range names {
			bots[i] = bot.Level(strings.TrimSpace(req.Bots[i]))
			// Боту без имени комната сама подберёт имя из лора.
			if bots[i] != "" && strings.TrimSpace(names[i]) == "" {
				continue
			}
			names[i] = cleanName(names[i], fmt.Sprintf("Игрок %d", i+1))
		}
		p, err = rm.JoinHotseat(names, bots)
	} else {
		p, err = rm.Join(cleanName(req.Name, "Игрок 1"))
	}
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, joinResponse{Room: rm.ID, Mode: rm.Mode, Token: p.Token, Seat: p.Seat, Name: p.Name})
}

func (s *Server) handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	p, err := rm.Join(cleanName(req.Name, "Игрок"))
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, joinResponse{Room: rm.ID, Mode: rm.Mode, Token: p.Token, Seat: p.Seat, Name: p.Name})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	var req struct {
		Token  string      `json:"token"`
		Action game.Action `json:"action"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := rm.Act(req.Token, req.Action); err != nil {
		writeRoomError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBot управляет ботами комнаты: посадить в лобби, убрать, сменить
// уровень или отдать боту место ушедшего игрока.
func (s *Server) handleBot(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"`
		Op    string `json:"op"` // add | remove | level | replace
		Seat  int    `json:"seat"`
		Level string `json:"level"`
	}
	if !decode(w, r, &req) {
		return
	}
	level := bot.Level(req.Level)
	var err error
	switch req.Op {
	case "add":
		_, err = rm.AddBot(req.Token, level)
	case "remove":
		err = rm.RemoveBot(req.Token, req.Seat)
	case "level":
		err = rm.SetBotLevel(req.Token, req.Seat, level)
	case "replace":
		err = rm.ReplaceWithBot(req.Token, req.Seat, level)
	default:
		writeError(w, http.StatusBadRequest, "неизвестная операция с ботом")
		return
	}
	if err != nil {
		writeRoomError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleLeave выпускает игрока из комнаты. Клиент после этого забывает
// токен, поэтому ответ сообщает, освободилось ли место совсем.
func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	freed, err := rm.Leave(req.Token)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"freed": freed})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	var req struct {
		Token   string `json:"token"`
		Text    string `json:"text"`
		Channel string `json:"channel"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := rm.Say(req.Token, req.Text, req.Channel); err != nil {
		writeRoomError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleVoice отмечает вход в звонок и выход из него.
func (s *Server) handleVoice(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	var req struct {
		Token  string `json:"token"`
		Active bool   `json:"active"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := rm.SetVoice(req.Token, req.Active); err != nil {
		writeRoomError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSignal перекладывает сигнал WebRTC соседу по комнате. Сервер не
// разбирает содержимое: для него это непрозрачный блок данных.
func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	var req struct {
		Token string          `json:"token"`
		To    int             `json:"to"`
		Data  json.RawMessage `json:"data"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Data) == 0 {
		writeError(w, http.StatusBadRequest, "пустой сигнал")
		return
	}
	if err := rm.Signal(req.Token, req.To, req.Data); err != nil {
		writeRoomError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(rm.Snapshot(r.URL.Query().Get("token")))
}

// handleEvents — поток состояний (SSE). Клиентский EventSource сам
// переподключается, поэтому отдельная логика реконнекта не нужна.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	rm, ok := s.room(w, r)
	if !ok {
		return
	}
	subID, ch, err := rm.Subscribe(r.URL.Query().Get("token"))
	if err != nil {
		writeRoomError(w, err)
		return
	}
	defer rm.Unsubscribe(subID)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // чтобы nginx не буферизовал поток
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	_ = rc.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			if !s.sseWrite(rc, w, msg.Event, msg.Data) {
				return
			}
		case <-heartbeat.C:
			// Комментарий-пустышка: держит соединение живым через прокси.
			if !s.sseComment(rc, w) {
				return
			}
		}
	}
}

func (s *Server) sseWrite(rc *http.ResponseController, w http.ResponseWriter, event string, data []byte) bool {
	_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return false
	}
	return rc.Flush() == nil
}

func (s *Server) sseComment(rc *http.ResponseController, w http.ResponseWriter) bool {
	_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
		return false
	}
	return rc.Flush() == nil
}

// --- вспомогательное ---

func (s *Server) room(w http.ResponseWriter, r *http.Request) (*room.Room, bool) {
	id := strings.ToUpper(r.PathValue("id"))
	rm, err := s.rooms.Get(id)
	if err != nil {
		writeRoomError(w, err)
		return nil, false
	}
	return rm, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "не удалось разобрать JSON: "+err.Error())
		return false
	}
	return true
}

func cleanName(raw, fallback string) string {
	name := strings.TrimSpace(raw)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	if name == "" {
		return fallback
	}
	if len([]rune(name)) > maxNameLen {
		name = string([]rune(name)[:maxNameLen])
	}
	return name
}

// writeRoomError переводит ошибки доменного слоя в коды HTTP.
func writeRoomError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, room.ErrNoRoom):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, room.ErrBadToken):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, room.ErrRoomFull), errors.Is(err, room.ErrNameTaken),
		errors.Is(err, room.ErrStarted), errors.Is(err, room.ErrSeatBusy), errors.Is(err, room.ErrNotBot):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, room.ErrBadSeat), errors.Is(err, room.ErrNoSuchSeat),
		errors.Is(err, room.ErrNotListening):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, room.ErrTooMany):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, game.ErrNotYourTurn), errors.Is(err, room.ErrWrongPlayer):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
