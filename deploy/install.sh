#!/usr/bin/env bash
# Первичная установка «Осады» на VM без Docker.
# Запускать на самой VM от root, из распакованного пакета:
#   tar xzf siege-linux-amd64.tar.gz && cd siege && sudo ./deploy/install.sh
set -euo pipefail

APP_DIR=/opt/siege
ENV_FILE=/etc/siege.env
SERVICE=siege
USER_NAME=siege

if [[ $EUID -ne 0 ]]; then
  echo "Нужны права root: sudo ./deploy/install.sh" >&2
  exit 1
fi

if [[ ! -f ./siege ]]; then
  echo "Рядом со скриптом нет бинарника ./siege — запустите make package и распакуйте архив" >&2
  exit 1
fi

echo "==> Пользователь $USER_NAME"
if ! id -u "$USER_NAME" >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin "$USER_NAME"
fi

echo "==> Каталог $APP_DIR"
install -d -o "$USER_NAME" -g "$USER_NAME" -m 0755 "$APP_DIR"
# Бинарник заменяем атомарно: работающий процесс не ломается на полпути.
install -o root -g root -m 0755 ./siege "$APP_DIR/siege.new"
mv -f "$APP_DIR/siege.new" "$APP_DIR/siege"

echo "==> Конфигурация $ENV_FILE"
if [[ ! -f "$ENV_FILE" ]]; then
  install -o root -g "$USER_NAME" -m 0640 ./.env.example "$ENV_FILE"
  echo "    создан из .env.example — отредактируйте домен и SIEGE_ALLOWED_ORIGIN"
else
  echo "    уже существует, не трогаем"
fi

echo "==> systemd"
install -o root -g root -m 0644 ./deploy/siege.service "/etc/systemd/system/$SERVICE.service"
systemctl daemon-reload
systemctl enable "$SERVICE"
systemctl restart "$SERVICE"

echo "==> Проверка"
sleep 1
if "$APP_DIR/siege" -health; then
  echo "Готово: сервис отвечает. Логи: journalctl -u $SERVICE -f"
else
  echo "Сервис не отвечает. Смотрите: journalctl -u $SERVICE -n 50" >&2
  exit 1
fi
