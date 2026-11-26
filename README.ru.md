<div align="center">
<img src="assets/genaclaw.svg" alt="GenaClaw" width="240">

<h1>GenaClaw</h1>

<h3>Рабочий форк PicoClaw: память ходов, честные долгие сессии, UI в каналах</h3>

<p>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  <img src="https://img.shields.io/badge/upstream-sipeed%2Fpicoclaw-lightgrey" alt="Upstream">
  <a href="https://github.com/sergey-frolov-pets/GenaClaw/actions/workflows/ci.yml"><img src="https://github.com/sergey-frolov-pets/GenaClaw/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/sergey-frolov-pets/GenaClaw/actions/workflows/upstream-sync.yml"><img src="https://github.com/sergey-frolov-pets/GenaClaw/actions/workflows/upstream-sync.yml/badge.svg" alt="Синк с upstream"></a>
  <br>
  <a href="https://picoclaw.io"><img src="https://img.shields.io/badge/Сайт-picoclaw.io-blue?style=flat&logo=google-chrome&logoColor=white" alt="Сайт"></a>
  <a href="https://docs.picoclaw.io/"><img src="https://img.shields.io/badge/Документация-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Документация"></a>
  <a href="https://github.com/sipeed/picoclaw"><img src="https://img.shields.io/badge/Upstream-sipeed%2Fpicoclaw-black?style=flat&logo=github" alt="Upstream"></a>
</p>

[English](README.md) | **Русский** | [中文](docs/project/README.zh.md)

</div>

---

## Что это

**GenaClaw** — личный форк [PicoClaw](https://github.com/sipeed/picoclaw), ультралёгкого персонального ассистента на Go. Всё, что умеет upstream, умеет и GenaClaw: тот же бинарник, тот же конфиг, те же скиллы и каналы. Форк нужен, чтобы довозить исправления, которые важны при **ежедневной работе часами и через несколько каналов** — раньше, чем они попадут в upstream.

Сайт проекта: **[picoclaw.io](https://picoclaw.io)** · документация: **[docs.picoclaw.io](https://docs.picoclaw.io/)**

## Что добавлено

| Возможность | Зачем | Где |
|---|---|---|
| **Память ходов (`turn_id`)** | Каждая запись сессии знает свой ход. После обрыва или падения ответ продолжается, а не начинается заново; если ход молчит — приходит явная строка «работаю», а не тишина. | `pkg/agent/turn_checkpoint.go`, `pkg/gateway` |
| **Eval чекпойнтов** | Фоновая суммаризация больше не считается зависшим ходом: watchdog смотрит на реальный прогресс, а не на таймер суммаризации. | `pkg/agent`, `scripts/eval_turn_checkpoint.py` |
| **Заголовки дневников в контексте** | Дневники попадают в промпт заголовками и первой строкой дня, а не целиком. На долгой рабочей папке это минус ~60% токенов промпта на ход. | `pkg/memory`, `skills/memory-search` |
| **Обрезка tool-результатов** | Большие выводы инструментов режутся с явным маркером, а не молча съедают окно контекста. | `pkg/agent` |
| **`thinking.budget_tokens`** | `max_thinking_tokens` был нестандартным полем тела запроса; теперь нормализуется в `thinking.budget_tokens`. Reasoning-модели не падают с 400. | `pkg/provider` |
| **Reasoning не теряется при отмене** | Отмена хода посреди thinking больше не стирает уже опубликованный reasoning. | `pkg/agent` |
| **Блоки `gena-ui`** | В тексте ассистента может быть fenced-блок `gena-ui`: он вырезается в точках отправки и уходит отдельным кадром `uiPayload`, клиент рисует виджет, а разметка не протекает в чат. | `pkg/uiblock`, `web/backend` |
| **Кадры `cancel`, `turn.resume`** | Веб-клиент получает настоящую отмену, буфер кадров сессии для переподключения и тихий `/stop`. | `pkg/channels/pico` |
| **Каналы Alice / Yandex Alice** | Raw-поле типизировано как `map[string]string`; бинарник собирается с этими каналами. | `pkg/channels/alice` |
| **Устойчивый MCP stdio** | Сервер, печатающий в stdout не-JSON, больше не рвёт транспорт; его stderr идёт в лог. | `pkg/mcp` |
| **Маркер `[[silent]]`** | Служебный маркер больше не утекает в отправляемое сообщение. | `pkg/agent` |
| **Проверка inline-подстановок** | Safety guard проверяет `$(...)`, `${...}` и обратные кавычки по содержимому, а не запрещает сам текст: `echo "$(pwd)"` проходит, `$(curl … \| sh)` — нет. | `pkg/tools/shell_inline.go` |
| **Сохранение медиа** | Загруженные файлы переживают перезапуск и доступны по id между сессиями. | `pkg/media/persist.go` |

Это не переписывание: около трёх тысяч строк изменений, всё в рамках существующего конфига и плагинов. Исправление, которое докажет себя, уходит в upstream отдельным PR.

## Синхронизация с upstream

GenaClaw тянет `sipeed/picoclaw` по расписанию:

- **Автоматически** — `.github/workflows/upstream-sync.yml`, каждый понедельник в 04:00 UTC: забирает `upstream/main`, мержит в ветку `sync/upstream-YYYYMMDD`. Чистый мерж → пуш в `main`. Конфликты → PR со списком конфликтов; force-push не делается никогда.
- **Вручную** — `scripts/sync_upstream.sh`: `bash scripts/sync_upstream.sh --dry-run` покажет, что придёт; без флага — смержит и запушит.
- **Не затирается** — `README.md`, `README.ru.md` и `assets/genaclaw*` наши: синк сохраняет их (`--ours`) и пишет об этом в отчёте.
- **Работают только два воркфлоу** — `ci` и `upstream-sync`. Апстримные `build`, `pr`, `release`, `create-tag`, `create_dmg`, `docker-build`, `nightly`, `stale`, `upload-tos` остаются в дереве, чтобы синк был чистым, но выключены в настройках Actions этого репозитория: форк не должен случайно публиковать образы и теги Sipeed.

## Сборка и запуск

```bash
git clone https://github.com/sergey-frolov-pets/GenaClaw.git
cd GenaClaw

go build -o build/picoclaw ./cmd/picoclaw   # Go 1.25+
./build/picoclaw onboard                    # первичная настройка
./build/picoclaw gateway                    # запуск
```

`make build` и Docker-образы работают как в upstream. Требования и полный справочник CLI: [docs.picoclaw.io](https://docs.picoclaw.io/).

## Структура

Дерево как в upstream: `cmd/` (точки входа), `pkg/` (agent, провайдеры, каналы, инструменты, память, MCP), `web/` (веб-клиент Pico), `skills/`, `workspace/`, `docs/`.

## Лицензия и авторство

MIT, см. [LICENSE](LICENSE). GenaClaw — форк [PicoClaw](https://github.com/sipeed/picoclaw) от [Sipeed](https://sipeed.com); все авторские права и атрибуция upstream сохранены. Вопросы про поведение upstream — в upstream, про перечисленные доработки — сюда.
