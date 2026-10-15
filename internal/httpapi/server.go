// Package httpapi — транспорт: HTTP + Server-Sent Events.
// Правила игры живут в internal/game и ничего не знают про сеть.
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"siege/internal/bot"
	"siege/internal/config"
	"siege/internal/game"
	"siege/internal/room"
)

// SDP с собранными кандидатами ICE — самое большое тело в проекте.
const maxBodyBytes = 64 << 10

type Server struct {
	cfg      config.Config
	log      *slog.Logger
	rooms    *room.Manager
	web      fs.FS
	meta     []byte
	metaETag string
	started  time.Time
	limiter  *limiter
	signals  *limiter
	handler  http.Handler
}

func New(cfg config.Config, log *slog.Logger, rooms *room.Manager, web fs.FS) *Server {
	// Список ICE-серверов живёт в конфигурации, а нужен браузеру — цепляем
	// его к тем же неизменным данным, что и карта с характеристиками.
	// Пустую или битую строку заменяем на пустой список: конфигурацию
	// проверяет config.Load, а сервер из-за неё падать не должен.
	ice := strings.TrimSpace(cfg.IceServers)
	if !json.Valid([]byte(ice)) {
		ice = "[]"
	}
	meta, err := json.Marshal(struct {
		game.Meta
		IceServers json.RawMessage `json:"iceServers"`
		BotLevels  []bot.LevelInfo `json:"botLevels"`
	}{game.NewMeta(), json.RawMessage(ice), bot.Levels()})
	if err != nil {
		panic("httpapi: не удалось сериализовать meta: " + err.Error())
	}
	sum := sha256.Sum256(meta)
	s := &Server{
		cfg:      cfg,
		log:      log,
		rooms:    rooms,
		web:      web,
		meta:     meta,
		metaETag: `"` + hex.EncodeToString(sum[:8]) + `"`,
		started:  time.Now(),
		limiter:  newLimiter(30, 10*time.Second),
		// Установка звонка — это несколько сигналов подряд от каждого
		// собеседника, и упираться в общий лимит она не должна.
		signals: newLimiter(120, 10*time.Second),
	}
	s.handler = s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/rooms", s.handleCreateRoom)
	mux.HandleFunc("POST /api/rooms/{id}/join", s.handleJoinRoom)
	mux.HandleFunc("GET /api/rooms/{id}/events", s.handleEvents)
	mux.HandleFunc("GET /api/rooms/{id}/state", s.handleState)
	mux.HandleFunc("POST /api/rooms/{id}/action", s.handleAction)
	mux.HandleFunc("POST /api/rooms/{id}/leave", s.handleLeave)
	mux.HandleFunc("POST /api/rooms/{id}/bot", s.handleBot)
	mux.HandleFunc("POST /api/rooms/{id}/chat", s.handleChat)
	mux.HandleFunc("POST /api/rooms/{id}/voice", s.handleVoice)
	mux.HandleFunc("POST /api/rooms/{id}/signal", s.handleSignal)
	mux.HandleFunc("GET /api/meta", s.handleMeta)

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleHealth)
	mux.HandleFunc("GET /version", s.handleVersion)

	mux.Handle("GET /", s.staticHandler())

	return s.recoverer(s.logRequests(s.guard(mux)))
}

// --- middleware ---

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("паника в обработчике",
					"path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// SSE-поток живёт минутами: его логируем на debug, чтобы не шуметь.
		level := slog.LevelInfo
		if strings.HasSuffix(r.URL.Path, "/events") || rec.status < 400 && r.Method == http.MethodGet {
			level = slog.LevelDebug
		}
		s.log.Log(r.Context(), level, "запрос",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ms", time.Since(start).Milliseconds(), "ip", clientIP(r))
	})
}

// guard ограничивает размер тела, частоту запросов и проверяет Origin.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			if s.cfg.AllowedOrigin != "" {
				origin := strings.TrimRight(r.Header.Get("Origin"), "/")
				if origin != "" && origin != s.cfg.AllowedOrigin {
					writeError(w, http.StatusForbidden, "запрос с чужого источника")
					return
				}
			}
			lim := s.limiter
			if strings.HasSuffix(r.URL.Path, "/signal") {
				lim = s.signals
			}
			if !lim.allow(clientIP(r)) {
				writeError(w, http.StatusTooManyRequests, "слишком много запросов, подождите секунду")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- статика ---

// Таблицу типов Go достраивает из /etc/mime.types, а в контейнере с игрой
// лежит один бинарник и больше ничего. Без этих двух строк манифест уезжает
// как text/plain, и браузер отказывается ставить игру на домашний экран.
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".m4a", "audio/mp4")
}

func (s *Server) staticHandler() http.Handler {
	fileServer := http.FileServerFS(s.web)
	dev := s.cfg.WebDir != ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dev {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.Contains(r.URL.Path, ".") {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// --- служебные ручки ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"uptimeS": int(time.Since(s.started).Seconds()),
		"rooms":   s.rooms.Count(),
		"version": s.cfg.Version,
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.cfg.Version})
}

// handleMeta отдаёт карту и характеристики. Кэш только с перепроверкой:
// после выкладки с изменённым балансом или рельефом клиент обязан получить
// новую версию, иначе он нарисует поле, которого больше нет.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("ETag", s.metaETag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == s.metaETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(s.meta)
}

// --- вспомогательное ---

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap нужен, чтобы http.NewResponseController дошёл до реального writer
// (иначе SSE не сможет флашить).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func clientIP(r *http.Request) string {
	// За nginx реальный адрес приходит в X-Forwarded-For.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
