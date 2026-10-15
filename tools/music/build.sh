#!/usr/bin/env bash
# Собирает музыку игры и кладёт выбранную пару в web/audio.
#
#   tools/music/build.sh                      — пересобрать то, что стоит сейчас
#   tools/music/build.sh --menu X --game Y    — поставить другую пару
#   tools/music/build.sh --game none          — убрать музыку оттуда совсем
#   tools/music/build.sh --all                — отрендерить все десять на послушать
#
# Сами треки в git не лежат: генератор детерминирован, и десять wav весят
# больше, чем весь остальной репозиторий. В web/audio едет только выбранная
# пара, уже в m4a — её вшивает в бинарник assets.go.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out="$root/build/music"
menu=menu-2-kamennye-vorota
game=none            # none — экран играет в тишине
all=0

while [ $# -gt 0 ]; do
  case "$1" in
    --menu) menu="$2"; shift 2 ;;
    --game) game="$2"; shift 2 ;;
    --out)  out="$2";  shift 2 ;;
    --all)  all=1;     shift ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "неизвестный ключ: $1" >&2; exit 2 ;;
  esac
done

python3 -c 'import numpy, scipy' 2>/dev/null || {
  echo "нужны numpy и scipy: python3 -m pip install numpy scipy" >&2; exit 1; }

mkdir -p "$out"
if [ "$all" = 1 ]; then
  python3 "$here/compose.py" "$out"
else
  want=""
  for slug in "$menu" "$game"; do
    [ "$slug" = none ] && continue
    want="${want:+$want,}$slug"
  done
  [ -n "$want" ] || { echo "обе стороны none: нечего собирать" >&2; exit 2; }
  python3 "$here/compose.py" "$out" --only "$want"
fi

# Кодек: на маке свой, на остальных ffmpeg. Браузеры одинаково играют AAC.
encode() { # wav m4a
  if command -v afconvert >/dev/null 2>&1; then
    afconvert -f m4af -d aac -b 128000 "$1" "$2"
  elif command -v ffmpeg >/dev/null 2>&1; then
    ffmpeg -nostdin -loglevel error -y -i "$1" -c:a aac -b:a 128k "$2"
  else
    echo "нечем кодировать: нужен afconvert (macOS) или ffmpeg" >&2; exit 1
  fi
}

for w in "$out"/*.wav; do encode "$w" "${w%.wav}.m4a"; done

if [ "$all" = 1 ]; then
  echo
  echo "Все треки: $out"
  echo "Поставить пару: tools/music/build.sh --menu <slug> --game <slug>"
  exit 0
fi

mkdir -p "$root/web/audio"
for kind in menu game; do
  eval "slug=\$$kind"
  if [ "$slug" = none ]; then
    rm -f "$root/web/audio/$kind.m4a"   # экран остаётся без музыки
  else
    cp "$out/$slug.m4a" "$root/web/audio/$kind.m4a"
  fi
done

# Длина петли уезжает рядом с музыкой: music.js читает её оттуда, чтобы
# loopEnd не пришлось править руками при смене трека.
MENU="$menu" GAME="$game" OUT="$out" ROOT="$root" python3 - <<'PY'
import json, os, pathlib
src = json.load(open(os.path.join(os.environ["OUT"], "tracks.json"), encoding="utf-8"))
by = {t["slug"]: t for t in src}
meta = {}
for kind, env in (("menu", "MENU"), ("game", "GAME")):
    slug = os.environ[env]
    if slug == "none":
        print(f"{kind}: без музыки")
        continue
    t = by[slug]
    meta[kind] = {"slug": t["slug"], "title": t["title"], "loop": t["loop"]}
    print(f"{kind}: {t['title']} ({slug}), петля {t['loop']} с")
path = pathlib.Path(os.environ["ROOT"], "web", "audio", "tracks.json")
path.write_text(json.dumps(meta, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
PY
echo "Готово: web/audio"
