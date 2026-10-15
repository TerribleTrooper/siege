// Package config читает настройки из переменных окружения: на VM так проще
// всего — systemd EnvironmentFile или docker --env-file, без правки кода.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr          string        // адрес прослушивания, напр. ":8080"
	PublicURL     string        // внешний адрес, только для подсказок в логах
	LogLevel      slog.Level    // debug|info|warn|error
	LogFormat     string        // text|json
	AllowedOrigin string        // пусто — не проверять Origin
	MaxRooms      int           // предел одновременных комнат
	IdleTTL       time.Duration // через сколько удалять пустую комнату
	TurnTimeout   time.Duration // 0 — ходы без ограничения времени
	BotDelay      time.Duration // пауза между действиями бота на глазах у людей
	WebDir        string        // отдавать фронтенд с диска (разработка)
	IceServers    string        // JSON-массив серверов ICE для голосовых звонков
	ShutdownWait  time.Duration // сколько ждать завершения запросов
	Version       string
}

// Load собирает конфигурацию и валидирует её. Ошибка означает, что сервер
// не должен стартовать: лучше упасть сразу, чем работать неправильно.
func Load(version string) (Config, error) {
	c := Config{
		Addr:          env("SIEGE_ADDR", ":8080"),
		PublicURL:     env("SIEGE_PUBLIC_URL", ""),
		LogFormat:     strings.ToLower(env("SIEGE_LOG_FORMAT", "text")),
		AllowedOrigin: strings.TrimRight(env("SIEGE_ALLOWED_ORIGIN", ""), "/"),
		WebDir:        env("SIEGE_WEB_DIR", ""),
		IceServers:    env("SIEGE_ICE_SERVERS", defaultIceServers),
		Version:       version,
	}

	// PORT уважаем ради хостингов, которые задают только его.
	if port := os.Getenv("PORT"); port != "" && os.Getenv("SIEGE_ADDR") == "" {
		c.Addr = ":" + port
	}

	var err error
	if c.LogLevel, err = parseLevel(env("SIEGE_LOG_LEVEL", "info")); err != nil {
		return c, err
	}
	if c.MaxRooms, err = envInt("SIEGE_MAX_ROOMS", 200); err != nil {
		return c, err
	}
	if c.IdleTTL, err = envDuration("SIEGE_ROOM_TTL", 2*time.Hour); err != nil {
		return c, err
	}
	if c.TurnTimeout, err = envDuration("SIEGE_TURN_TIMEOUT", 0); err != nil {
		return c, err
	}
	if c.BotDelay, err = envDuration("SIEGE_BOT_DELAY", 350*time.Millisecond); err != nil {
		return c, err
	}
	if c.ShutdownWait, err = envDuration("SIEGE_SHUTDOWN_WAIT", 10*time.Second); err != nil {
		return c, err
	}

	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, fmt.Errorf("SIEGE_LOG_FORMAT: ожидалось text или json, получено %q", c.LogFormat)
	}
	if c.MaxRooms <= 0 {
		return c, fmt.Errorf("SIEGE_MAX_ROOMS должно быть больше нуля")
	}
	if c.TurnTimeout != 0 && c.TurnTimeout < 10*time.Second {
		return c, fmt.Errorf("SIEGE_TURN_TIMEOUT: минимум 10s (или 0, чтобы отключить)")
	}
	if c.BotDelay > 5*time.Second {
		return c, fmt.Errorf("SIEGE_BOT_DELAY: не больше 5s — иначе ход бота длится минуту")
	}
	if c.WebDir != "" {
		if st, err := os.Stat(c.WebDir); err != nil || !st.IsDir() {
			return c, fmt.Errorf("SIEGE_WEB_DIR=%q: каталог недоступен", c.WebDir)
		}
	}
	// Список ICE уезжает в браузер как есть, поэтому проверяем его на старте,
	// а не когда у игроков молча перестанут соединяться звонки.
	var probe []map[string]any
	if err := json.Unmarshal([]byte(c.IceServers), &probe); err != nil {
		return c, fmt.Errorf("SIEGE_ICE_SERVERS: ожидался JSON-массив вида "+
			`[{"urls":"stun:..."}], получено %q: %w`, c.IceServers, err)
	}
	if len(probe) == 0 {
		return c, fmt.Errorf("SIEGE_ICE_SERVERS: список пуст, звонки не соединятся")
	}
	return c, nil
}

// defaultIceServers — публичный STUN Google: его хватает, пока оба
// собеседника не за симметричным NAT. Для надёжности нужен свой TURN,
// см. docs/DEPLOY.md.
const defaultIceServers = `[{"urls":"stun:stun.l.google.com:19302"}]`

// Logger строит логгер по конфигурации.
func (c Config) Logger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	raw := env(key, "")
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: ожидалось число, получено %q", key, raw)
	}
	return v, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	raw := env(key, "")
	if raw == "" {
		return def, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: ожидалась длительность вида 90s или 2h, получено %q", key, raw)
	}
	if v < 0 {
		return 0, fmt.Errorf("%s: длительность не может быть отрицательной", key)
	}
	return v, nil
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("SIEGE_LOG_LEVEL: неизвестный уровень %q", raw)
}
