// Команда server — единственный бинарник проекта: правила, API и фронтенд.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"siege"
	"siege/internal/config"
	"siege/internal/httpapi"
	"siege/internal/room"
)

// version подставляется при сборке: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if err := run(); err != nil {
		// Логгер может быть ещё не создан, поэтому пишем в stderr.
		os.Stderr.WriteString("осада: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	// -health нужен для HEALTHCHECK в образе без оболочки (distroless):
	// бинарник сам себя проверяет по HTTP и возвращает код выхода.
	health := flag.Bool("health", false, "проверить живость сервера и выйти")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	cfg, err := config.Load(buildVersion())
	if err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(cfg.Version)
		return nil
	}
	if *health {
		return healthcheck(cfg.Addr)
	}
	log := cfg.Logger()

	web, err := webFS(cfg)
	if err != nil {
		return err
	}

	// Контекст живёт до SIGINT/SIGTERM: systemd и docker stop присылают SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rooms := room.NewManager(room.Options{
		MaxRooms:    cfg.MaxRooms,
		IdleTTL:     cfg.IdleTTL,
		TurnTimeout: cfg.TurnTimeout,
		BotDelay:    cfg.BotDelay,
	}, log)
	go rooms.Run(ctx)

	api := httpapi.New(cfg, log, rooms, web)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: api.Handler(),
		// ReadTimeout и WriteTimeout намеренно не заданы: они обрывают
		//долгий SSE-поток. Дедлайны ставятся на каждую запись в обработчике.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("сервер запущен",
			"addr", cfg.Addr, "version", cfg.Version,
			"turnTimeout", cfg.TurnTimeout.String(), "web", webSource(cfg))
		if cfg.PublicURL != "" {
			log.Info("внешний адрес", "url", cfg.PublicURL)
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("получен сигнал остановки, завершаем запросы", "wait", cfg.ShutdownWait.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("не все соединения закрылись штатно", "err", err)
		return srv.Close()
	}
	log.Info("сервер остановлен")
	return nil
}

// webFS выбирает источник фронтенда: каталог на диске (разработка) или
// вшитые файлы (production).
func webFS(cfg config.Config) (fs.FS, error) {
	if cfg.WebDir != "" {
		return os.DirFS(cfg.WebDir), nil
	}
	return siege.WebFS(), nil
}

func webSource(cfg config.Config) string {
	if cfg.WebDir != "" {
		return "disk:" + cfg.WebDir
	}
	return "embedded"
}

// buildVersion берёт версию из -ldflags, иначе из метаданных модуля.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	rev, dirty := "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				rev = s.Value[:7]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return version
	}
	return rev + dirty
}

// healthcheck ходит на собственный /healthz. Используется в HEALTHCHECK
// Docker-образа и в скриптах проверки после деплоя.
func healthcheck(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("не разобрать адрес %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s/healthz", net.JoinHostPort(host, port))
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("сервер не отвечает: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("сервер ответил %s", strings.TrimSpace(res.Status))
	}
	return nil
}
