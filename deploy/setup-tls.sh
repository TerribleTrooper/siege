#!/usr/bin/env bash
# Настройка HTTPS для «Осады»: nginx + сертификат Let's Encrypt.
#
# Запускать на самой VM от root, после deploy/install.sh:
#   sudo ./deploy/setup-tls.sh qwety.space ваша@почта
#
# Скрипт идемпотентен: повторный запуск ничего не ломает.
set -euo pipefail

DOMAIN="${1:-}"
EMAIL="${2:-}"
SITE=/etc/nginx/sites-available/siege
ENV_FILE=/etc/siege.env

if [[ -z "$DOMAIN" ]]; then
  echo "Использование: sudo ./deploy/setup-tls.sh ДОМЕН [почта]" >&2
  exit 1
fi
if [[ $EUID -ne 0 ]]; then
  echo "Нужны права root: sudo ./deploy/setup-tls.sh $DOMAIN" >&2
  exit 1
fi
if [[ ! -f ./deploy/nginx.conf ]]; then
  echo "Запускайте из распакованного пакета: рядом должен лежать deploy/nginx.conf" >&2
  exit 1
fi

echo "==> Проверяю, что $DOMAIN смотрит на эту машину"
resolved="$(getent ahostsv4 "$DOMAIN" | awk '{print $1}' | sort -u | head -5 || true)"
if [[ -z "$resolved" ]]; then
  cat >&2 <<MSG
Домен $DOMAIN не разрешается в IP.

Пока у домена нет A-записи, Let's Encrypt не сможет его проверить.
В панели регистратора пропишите A-запись на внешний адрес этой машины
и подождите, пока обновится DNS (от 15 минут до нескольких часов).
MSG
  exit 1
fi
# Внешний адрес за NAT не совпадает с локальным, поэтому спрашиваем его
# у облака и у публичных сервисов: какой-то из них может быть недоступен.
detect_public_ip() {
  local ip url
  # Метаданные облака — самый надёжный источник внутри виртуальной машины.
  for url in \
    "http://169.254.169.254/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip" \
    "http://169.254.169.254/latest/meta-data/public-ipv4"
  do
    ip="$(curl -fsS --max-time 4 -H 'Metadata-Flavor: Google' "$url" 2>/dev/null | tr -d '[:space:]')"
    if [[ "$ip" =~ ^[0-9.]+$ ]]; then
      echo "$ip"
      return
    fi
  done
  for url in https://ifconfig.me/ip https://api.ipify.org https://icanhazip.com; do
    ip="$(curl -fsS --max-time 6 "$url" 2>/dev/null | tr -d '[:space:]')"
    if [[ "$ip" =~ ^[0-9.]+$ ]]; then
      echo "$ip"
      return
    fi
  done
}

public="$(detect_public_ip)"
local_ips="$(hostname -I 2>/dev/null || true)"
match=""
for ip in $resolved; do
  if [[ "$ip" == "$public" ]] || [[ " $local_ips " == *" $ip "* ]]; then
    match="$ip"
  fi
done
if [[ -n "$match" ]]; then
  echo "    $DOMAIN -> $match, совпадает"
elif [[ -z "$public" ]]; then
  # Не смогли узнать свой адрес — это ещё не повод останавливаться:
  # настоящую проверку всё равно сделает Let's Encrypt.
  echo "    внешний адрес машины определить не удалось, продолжаю: решит certbot"
else
  cat >&2 <<MSG
$DOMAIN указывает на: $(echo "$resolved" | tr '\n' ' ')
а эта машина доступна как: $public (локально: ${local_ips:-?})

Адреса не совпадают — сертификат выпустить не получится.
Поправьте A-запись и запустите скрипт снова.
MSG
  exit 1
fi

echo "==> Ставлю nginx и certbot"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq nginx certbot python3-certbot-nginx >/dev/null

echo "==> Открываю порты 80 и 443"
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  ufw allow 'Nginx Full' >/dev/null || true
fi

echo "==> Получаю сертификат Let's Encrypt"
if [[ -d "/etc/letsencrypt/live/$DOMAIN" ]]; then
  echo "    сертификат уже есть, пропускаю"
else
  # Первый выпуск делаем на стандартном конфиге nginx: наш ссылается на
  # файлы сертификата, которых до этого момента ещё не существует.
  if [[ -n "$EMAIL" ]]; then
    certbot --nginx -d "$DOMAIN" --agree-tos -m "$EMAIL" --no-eff-email --non-interactive
  else
    certbot --nginx -d "$DOMAIN" --agree-tos --register-unsafely-without-email --non-interactive
  fi
fi

echo "==> Ставлю конфиг nginx под $DOMAIN"
sed "s/siege.example.com/$DOMAIN/g" ./deploy/nginx.conf > "$SITE"
ln -sf "$SITE" /etc/nginx/sites-enabled/siege
rm -f /etc/nginx/sites-enabled/default
nginx -t
systemctl reload nginx

echo "==> Прописываю домен в $ENV_FILE"
if [[ -f "$ENV_FILE" ]]; then
  sed -i "s|siege\.example\.com|$DOMAIN|g" "$ENV_FILE"
  systemctl restart siege
else
  echo "    $ENV_FILE не найден — сначала выполните deploy/install.sh" >&2
fi

echo "==> Проверяю"
sleep 2
code="$(curl -s -o /dev/null -w '%{http_code}' "https://$DOMAIN/healthz" || true)"
if [[ "$code" == "200" ]]; then
  echo
  echo "Готово: https://$DOMAIN"
  echo "Голосовые звонки заработают — сайт отдаётся по https."
else
  echo "Сайт ответил кодом $code. Смотрите: journalctl -u siege -n 50; nginx -t" >&2
  exit 1
fi
