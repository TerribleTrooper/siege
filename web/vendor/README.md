# Сторонний код

## simplepeer.min.js

Обёртка над WebRTC для голосовой связи. Положена сюда целиком, а не через
npm: у проекта нет сборки фронтенда, и всё, что лежит в `web/`, вшивается
в бинарник. Так зависимость видна в истории git и не требует сети при сборке.

| | |
|---|---|
| Проект | https://github.com/feross/simple-peer |
| Версия | 9.11.1 |
| Коммит | `9ea1805d992a8164a42b750160ed3425f2a494f1` |
| Файл | `simplepeer.min.js` (собранный UMD-бандл из репозитория) |
| Лицензия | MIT, см. `simple-peer-LICENSE.txt` |
| sha256 | `de8330796ae774276491ac796aed42ea0141cdef43e46e90dedd2499a5d1b3e1` |

Обновление:

```bash
SHA=<новый коммит>
curl -sSL -o web/vendor/simplepeer.min.js \
  "https://raw.githubusercontent.com/feross/simple-peer/$SHA/simplepeer.min.js"
shasum -a 256 web/vendor/simplepeer.min.js   # сверить с таблицей выше
```
