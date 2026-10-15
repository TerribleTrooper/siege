# Сборка: статический бинарник без CGO, фронтенд вшит через go:embed.
FROM golang:1.25-alpine AS build

WORKDIR /src

# Зависимостей нет, но слой с go.mod кэшируется отдельно — так будет и дальше.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/siege ./cmd/server

# Проверяем на этапе сборки: битый образ до VM не доедет.
RUN go vet ./... && go test ./...

# Запуск: образ без оболочки и пакетов — меньше поверхность атаки.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/siege /siege

ENV SIEGE_ADDR=":8080" \
    SIEGE_LOG_FORMAT="json"

EXPOSE 8080
USER nonroot:nonroot

# В образе нет curl и shell, поэтому бинарник проверяет себя сам.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/siege", "-health"]

ENTRYPOINT ["/siege"]
