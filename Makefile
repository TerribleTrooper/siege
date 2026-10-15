# Сборка и выкладка «Осады».
#   make run             — локальный сервер на :8080
#   make test            — тесты и go vet
#   make package         — архив с бинарником под Linux и файлами деплоя
#   make deploy HOST=... — выложить на VM по ssh

APP      := siege
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  := -trimpath
BIN      := bin/$(APP)
DIST     := dist

# Куда выкладывать: make deploy HOST=user@1.2.3.4
HOST     ?=
ARCH     ?= amd64

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## Запустить локально (фронтенд с диска, без пересборки)
	SIEGE_WEB_DIR=web SIEGE_LOG_LEVEL=debug go run ./cmd/server

.PHONY: build
build: ## Собрать бинарник под текущую систему
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/server

.PHONY: test
test: ## go vet + тесты
	go vet ./...
	go test ./... -count=1

.PHONY: race
race: ## Тесты с детектором гонок (важно: комнаты работают из многих горутин)
	go test ./... -race -count=1

.PHONY: fmt
fmt: ## Форматирование
	gofmt -w $(shell find . -name '*.go' -not -path './dist/*')

.PHONY: music
music: ## Пересобрать музыку и поставить пару: make music MENU=... GAME=...
	tools/music/build.sh $(if $(MENU),--menu $(MENU)) $(if $(GAME),--game $(GAME))

.PHONY: music-all
music-all: ## Отрендерить все десять треков в build/music — послушать и выбрать
	tools/music/build.sh --all

.PHONY: build-linux
build-linux: ## Собрать статический бинарник под Linux (ARCH=amd64|arm64)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP) ./cmd/server

.PHONY: package
package: test build-linux ## Собрать архив для выкладки на VM
	@mkdir -p $(DIST)/pkg/deploy
	@cp $(DIST)/$(APP) $(DIST)/pkg/
	@cp .env.example $(DIST)/pkg/
	@cp deploy/siege.service deploy/nginx.conf deploy/install.sh deploy/setup-tls.sh $(DIST)/pkg/deploy/
	@# COPYFILE_DISABLE: иначе macOS кладёт в архив свои xattr, и tar на
	@# сервере сыплет предупреждениями про LIBARCHIVE.xattr.
	@cd $(DIST)/pkg && COPYFILE_DISABLE=1 tar czf ../$(APP)-linux-$(ARCH).tar.gz .
	@rm -rf $(DIST)/pkg
	@echo "Готово: $(DIST)/$(APP)-linux-$(ARCH).tar.gz ($(VERSION))"

.PHONY: deploy
deploy: package ## Выложить на VM: make deploy HOST=user@1.2.3.4
	@test -n "$(HOST)" || (echo "Укажите HOST=user@адрес" && exit 1)
	scp $(DIST)/$(APP)-linux-$(ARCH).tar.gz $(HOST):/tmp/
	@# -t нужен, чтобы sudo на той стороне мог спросить пароль.
	ssh -t $(HOST) 'set -e; \
	  rm -rf /tmp/siege-pkg && mkdir -p /tmp/siege-pkg && \
	  tar xzf /tmp/$(APP)-linux-$(ARCH).tar.gz -C /tmp/siege-pkg && \
	  cd /tmp/siege-pkg && sudo ./deploy/install.sh'
	@echo "Выложено $(VERSION) на $(HOST)"

.PHONY: docker
docker: ## Собрать образ
	docker build --build-arg VERSION=$(VERSION) -t $(APP):$(VERSION) -t $(APP):latest .

.PHONY: docker-run
docker-run: docker ## Собрать и запустить образ локально на :8080
	docker run --rm -p 8080:8080 -e SIEGE_LOG_FORMAT=text $(APP):$(VERSION)

.PHONY: clean
clean: ## Убрать артефакты сборки
	rm -rf bin dist
