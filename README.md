# Hackathon Live Scoring Platform

MVP платформа для хакатонов: собирает автоматические и ручные метрики по командам,
считает единый объяснимый score и показывает его в интерактивном Live Leaderboard.

Хакатон делится на **треки** (задачи/номинации): у каждого трека свои метрики,
веса, критерии, `min_judges_per_team`, лок скоринга и отдельный лидерборд с честными рангами.
Финализация при этом общая на весь хакатон.

Каждый итоговый балл воспроизводим и раскладывается на исходные метрики, веса,
формулы и историю изменений (`score_runs` + `config_hash` + breakdown на команду).

## Стек

- **Backend**: Go 1.26, `chi`, `pgx/v5`, PostgreSQL 17, embedded SQL-миграции, SSE, `LISTEN/NOTIFY` fan-out
- **Scoring engine**: изолированный пакет `backend/internal/scoring` с unit-тестами
  (нормализация `range/target/binary/manual`, MAD-outliers, completeness/provisional)
- **Frontend**: TanStack Start (React 19 + SSR), TanStack Router, TanStack Query, TypeScript, Gravity UI (`@gravity-ui/uikit`)
- **Infra**: Docker Compose (6 сервисов), nginx reverse proxy (`/api/*` → Go API, остальное → frontend SSR)

## Быстрый старт

```bash
docker compose up -d --build                  # db + api + worker + frontend + proxy
docker compose run --rm --entrypoint /usr/local/bin/seed api   # миграции + демо-данные
# опционально: живый поток метрик для demo
docker compose --profile demo up -d simulator
```

Демо-данные (`backend/cmd/seed/main.go`): хакатон `demo-hack-2026` в статусе `running`,
50 команд в 2 треках (`ai`, `min_judges=3` / `web`, `min_judges=2`), 5 автометрик
+ 5 критериев рубрики, 8 судей, 150 участников, 6 часов истории метрик (12 точек),
снапшоты динамики, ingest-источник `demo-simulator`.

- Public leaderboard: http://localhost/h/demo-hack-2026 (SSR)
- Страница команды: http://localhost/h/demo-hack-2026/teams/<teamId>
- API: http://localhost/api/healthz
- Judge UI: http://localhost/judge/demo-hack-2026
- Кабинет команды: http://localhost/team/demo-hack-2026
- Organizer console: `/app/hackathons/<id>` (ссылка «Панель организатора» на leaderboard для роли organizer)
- Все хакатоны + создание нового: http://localhost/ → «+ Create hackathon»
  (форма: slug, даты, min judges, опционально треки; дальше в консоли — метрики, рубрика,
  ingest-токены, назначение судей, статусы, лок скоринга, финализация)

> Важно: backend-образ имеет `ENTRYPOINT ["api"]`, поэтому `migrate`/`seed`
> запускаются только через `--entrypoint`, как выше:
> `docker compose run --rm --entrypoint /usr/local/bin/migrate api`.
> Цели `make migrate` / `make seed` этому правилу сейчас не следуют.

### Демо-аккаунты (пароль `demo1234`)

| Роль | Логин | Рабочее место |
|------|-------|---------------|
| Organizer | `organizer@demo.io` | консоль организатора `/app/hackathons/<id>` (ссылка «Панель организатора» на лидерборде) |
| Judges | `judge1@demo.io` … `judge8@demo.io` | панель судьи `/judge/demo-hack-2026` (лидерборд до финализации скрыт слепотой) |
| Team members | `member001@demo.io` … `member150@demo.io` | кабинет команды `/team/demo-hack-2026` |

Ingest-токен демо-хакатона: `demo-collector-token`.
Повторный `seed` без аргументов — no-op, если `demo-hack-2026` уже есть;
`--entrypoint /usr/local/bin/seed api reset` — стереть и пересеять демо-данные.

## Продакшен на маленьком VPS (1 ГБ RAM / 5 ГБ диск)

Проверено: влезает (~700 МБ RAM, ~1.1 ГБ образов), но только с оверлеем
`docker-compose.prod.yml` — лимиты памяти, тюнинг Postgres, БД без наружного
порта. Десятки одновременных зрителей — да; 200 concurrent viewers из ТЗ — нет.

```bash
# 1. Swap 2 ГБ (страховка) и фаервол
sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile && \
  sudo mkswap /swapfile && sudo swapon /swapfile
sudo ufw allow 80/tcp && sudo ufw allow 29742/tcp && sudo ufw --force enable

# 2. Образы: НЕ собирать на сервере (vite build и go build хотят >1 ГБ RAM).
#    Собрать на своей машине и перенести:
docker compose -f docker-compose.yml -f docker-compose.prod.yml build
docker save hackathon-backend:deploy hackathon-frontend:deploy \
  | gzip | ssh -p 29742 user@server docker load
# базовые образы (postgres, nginx) сервер докачает сам (~500 МБ)

# 3. На сервере: поднять, смигрировать, посеять
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d db
docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm \
  --entrypoint /usr/local/bin/migrate api
docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm \
  --entrypoint /usr/local/bin/seed api
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
docker builder prune -f   # вернуть место от сборки, если собирали на сервере
```

Что меняет оверлей: `mem_limit` всем сервисам (api/worker 128m, frontend 320m,
db 300m, proxy 32m), Postgres `shared_buffers=64MB, max_connections=30` и др.,
порт БД наружу снят (`ports: !reset []`), `APP_ENV=prod`, `restart: unless-stopped`,
`API_URL=http://api:8080` для SSR.

## Локальная разработка

```bash
# Postgres из compose (хост-порт 5433 → контейнерный 5432)
docker compose up -d db

# backend (порт 8080)
cd backend
go run ./cmd/migrate
go run ./cmd/seed            # или: go run ./cmd/seed reset
go run ./cmd/api
go run ./cmd/worker          # пересчёт каждые 5 мин (RECALC_INTERVAL) + LISTEN score_dirty (debounce 5s)

# frontend (порт 3000, /api проксируется vite на localhost:8080)
cd frontend
npm install
npm run dev                  # vite dev; прод: npm run build && npm start (порт 3000)
```

### Тесты и нагрузка

```bash
cd backend
go test ./...                                                    # unit + E2E (нужен Postgres на :5433)
TEST_DATABASE_URL=postgres://hackathon:hackathon@localhost:5433/hackathon?sslmode=disable go test ./internal/e2e/... -v
go run ./cmd/loadtest -hackathon <id> -viewers 200 -duration 30s -target http://localhost   # HTTP-нагрузка на leaderboard
go run ./cmd/loadtest -hackathon <id> -scoring -target http://localhost                     # замер длительности scoring run
```

`loadtest -scoring` логинится как organizer (`-email`/`-password`) и дергает
`POST /api/hackathons/<id>/scoring/recalculate`.

## Архитектура

```
collector / CI (GitHub Actions)
        │  POST /api/v1/ingest/metrics  (Bearer ingest-токен, idempotent по external_event_id)
        ▼
   Go API ──pg_notify(score_dirty, track_id)──► Worker (debounce 5s, периодический recalc 5 мин)
        │                                              │
        │                              scoring engine (детерминированный, config_hash, per-track advisory lock)
        │                                              │
        │                              team_scores + score_snapshots
        │                                              │
        ◄────────pg_notify(score_updated)──────────────┘
        │
        └──SSE /api/hackathons/:id/leaderboard/events──► EventSource + TanStack Query invalidation
                                                          (fallback-опрос каждые 30s при обрыве)
```

Сервисы compose: `db` (5433:5432), `api` (:8080), `worker`, `simulator`
(только `--profile demo`), `frontend` (:3000), `proxy` (:80).
nginx: `location /api/` → `api:8080` (SSE: `proxy_buffering off`, `read_timeout 3600s`),
`location /` → `frontend:3000` с WebSocket-заголовками.

- **Scoring**: `FinalScore = Σ(normᵢ × weightᵢ) / Σ(weightᵢ)` по полному набору метрик
  трека (вес отсутствующей метрики остаётся в знаменателе, вклад 0 — без ренормализации);
  нормализация `range/target/binary/manual` → 0..100; округление до 2 знаков.
- **Manual-критерии**: агрегация оценок судей — 1 оценка: `single` (provisional);
  2–3: `median`; 4+: MAD-фильтрация выбросов `|s−m| > 3×1.4826×MAD`, затем mean
  (`trimmed_mean`, fallback `median_fallback`). `min_judges_per_team` задаётся на трек.
- **Stale/missing**: у автометрик `staleness_seconds` (seed: 1800) → статус `stale`;
  `valid_min/valid_max` в нормализации отбрасывают неправдоподобные значения
  (`status='invalid'`, последнее корректное сохраняется). Статусы breakdown:
  `fresh | stale | missing | provisional`; скор команды `complete | provisional`,
  `completeness` = доля заполненных required-метрик, `eligible = complete`.
- **Версионирование**: каждый пересчёт — `score_run` (per-track `version`, `config_hash`,
  `mode: live|final`, `status`); live идёт в leaderboard, final — immutable после finalize.
  Ранги считаются пер-трек внутри scoring run.
- **Локи и финализация**: `scoring_locked` — пер-трек (live-пересчёты останавливаются,
  finalize игнорирует лок); `POST /finalize` — на весь хакатон (advisory lock,
  final-раны по всем трекам, статус `finalized`, треки лочатся, ingest отклоняется с 409).
- **Leaderboard**: live — только `public=true` метрики; final — полный набор.
  Фильтры `?track=&status=&q=`; дельты `score_delta/rank_delta` и `sparkline` из `score_snapshots`.
- **Judge blindness**: судья (роль judge + активный judging) получает `judge_blind`
  на leaderboard/team-detail, пока хакатон не финализирован; SSR пробрасывает cookies,
  чтобы не утечь таблицу в серверный HTML.
- **Auth**: cookie-сессии + CSRF-токен (`GET /me` возвращает `csrf_token`,
  мутации требуют `X-CSRF-Token`); ingest — отдельный Bearer-токен на хакатон
  (`ingest_sources`, хранится sha256-хэш). Роли: organizer (`hackathons.organizer_id`),
  judge (`judge_assignments`), member (`team_members`).
- **Append-only**: `metric_values`, `judge_scores`, `audit_events`, `score_snapshots`
  защищены триггерами (только INSERT).
- **Audit**: scoring/judging/submissions/статусы/локи/финализация пишутся в `audit_events` с причиной.
- **Worker**: чистит протухшие сессии каждый час; интервал recalc — `RECALC_INTERVAL` (default 5m).

## API (база `/api`, см. `backend/internal/server/server.go`)

Публичное:

| Метод | Путь |
|-------|------|
| `GET` | `/healthz` |
| `POST` | `/auth/login` (+ `POST /auth/logout`, cookie+CSRF) |
| `POST` | `/v1/ingest/metrics` (Bearer ingest-токен) |
| `GET` | `/hackathons` |
| `GET` | `/hackathons/{id\|slug}` |
| `GET` | `/hackathons/{id}/teams`, `GET /hackathons/{id}/teams/{teamId}` |
| `GET` | `/hackathons/{id}/tracks` |
| `GET` | `/hackathons/{id}/submissions/{teamId}` |
| `GET` | `/hackathons/{id}/metrics` |
| `GET` | `/hackathons/{id}/leaderboard?track=&status=&q=` |
| `GET` | `/hackathons/{id}/leaderboard/{teamId}` (breakdown) |
| `GET` | `/hackathons/{id}/leaderboard/events` (SSE: `leaderboard.updated`, `team.updated`, `scoring.completed`, `scoring.error`, heartbeat) |

Авторизованное (cookie + `X-CSRF-Token`):

| Метод | Путь | Роль |
|-------|------|------|
| `GET` | `/me`, `/my-roles` | любая |
| `POST` | `/hackathons` (создание; slug `^[a-z0-9][a-z0-9-]{2,62}[a-z0-9]$`, иначе трек `general`) | любая залогиненная (становится organizer) |
| `POST` | `/hackathons/{id}/teams`, `GET /hackathons/{id}/my-team` | member |
| `PUT` | `/hackathons/{id}/submissions/{teamId}` | member (своя команда) |
| `PUT`/`GET` | `/hackathons/{id}/judging/scores/{teamId}` | judge (только назначенные команды) |
| `POST` | `/hackathons/{id}/judging/recusal` | judge |
| `GET` | `/hackathons/{id}/judging/my-assignments` | judge |
| `POST` | `/hackathons/{id}/metrics`, `POST /hackathons/{id}/rubric/criteria`, `POST /hackathons/{id}/tracks`, `PATCH /hackathons/{id}/tracks/{trackId}` | organizer |
| `GET` | `/hackathons/{id}/audit` | organizer |
| `POST`/`GET` | `/hackathons/{id}/ingest-sources` | organizer |
| `POST` | `/hackathons/{id}/scoring/recalculate?track=` | organizer |
| `POST` | `/hackathons/{id}/scoring/lock?track=` (`{locked, reason}`) | organizer |
| `POST` | `/hackathons/{id}/finalize` | organizer |
| `GET` | `/hackathons/{id}/score-runs?track=` | organizer |
| `POST` | `/hackathons/{id}/status` (`draft→running→judging`, назад `judging→running`; `finalized` — только через `/finalize`) | organizer |
| `GET` | `/hackathons/{id}/judging/assignments` | organizer |
| `POST` | `/hackathons/{id}/judging/auto-assign`, `POST /hackathons/{id}/judging/assign` | organizer |

## Ingest API

```bash
curl -X POST http://localhost/api/v1/ingest/metrics \
  -H "Authorization: Bearer <ingest token>" -H "Content-Type: application/json" \
  -d '{"hackathon_id":"...","team_id":"...","metric_key":"api_latency",
       "value":124.3,"external_event_id":"ci-run-123",
       "captured_at":"2026-09-17T10:00:00Z","metadata":{}}'
```

Повторная доставка того же `external_event_id` не создаёт дубликат.
Токен привязан к хакатону (`token is not valid for this hackathon` при чужом `hackathon_id`);
после finalize метрики отклоняются (`409`). Значения вне `valid_min/valid_max`
сохраняются как `invalid` и не затирают последнее корректное.
Пример GitHub Actions workflow: `deploy/github-actions-benchmark.yml`.
Живой генератор трафика для демо: сервис `simulator`
(`SIM_API_URL`, `SIM_TOKEN`, `SIM_INTERVAL`, default 30s).

## Frontend-страницы (`frontend/src/routes`)

| Путь | Страница |
|------|----------|
| `/` | список хакатонов + «+ Create hackathon» |
| `/h/$slug` | leaderboard трека (SSR, поиск, фильтры, drawer с breakdown, ссылка на команду) |
| `/h/$slug/teams/$teamId` | страница команды (метрики, сабмишен, динамика) |
| `/judge/$slug` | панель судьи (только роль judge: назначения, оценки, самоотвод; организатору и участникам показывает объяснение со ссылками) |
| `/team/$slug` | кабинет команды (участник/зритель: создание команды и сабмишен; организатору и судье показывает объяснение — у них здесь нет рабочих мест) |
| `/hackathons/new` | создание хакатона (любой залогиненный; создатель становится organizer) |
| `/app/hackathons/$id` | консоль организатора (только роль organizer; остальным — объяснение со ссылками на их рабочие места) |
| `/login` | вход |

API-клиент: `frontend/src/lib/api.ts` (same-origin в браузере; в SSR — `API_URL`,
иначе build-time `VITE_API_URL`, иначе `http://localhost:8080`; cookies SSR
пробрасываются из входящего запроса). Live-обновления: `useLeaderboardEvents`
(`frontend/src/lib/sse.ts`).

## Роли и доступ

Роль всегда привязана к конкретному хакатону — глобальных ролей нет. Один и тот же
пользователь может быть организатором одного хакатона, судьёй другого и участником
третьего. При пересечении ролей в одном хакатоне побеждает старшая
(`organizer > judge > team_member`, см. `GET /api/my-roles`). Свою роль в открытом
хакатоне пользователь видит как бейдж «ваша роль» на лидерборде и в рабочих кабинетах.

| Роль | Что видит и может делать | Куда не пускаем (и почему) |
|------|--------------------------|----------------------------|
| Гость (не залогинен) | Список хакатонов, публичный лидерборд, страница команды | Кабинеты, судейство, создание хакатона — сначала «Войти» |
| Зритель (залогинен, без роли в хакатоне) | То же + может создать команду в `/team/$slug` (станет участником) и создать свой хакатон | Панель судьи и консоль организатора показывают объяснение со ссылками, а не пустоту |
| Участник (`team_member`) | Лидерборд + кабинет команды `/team/$slug`: создание команды (одна на человека), редактирование сабмишена своей команды | `/judge/*` («судьи не участвуют командами» наоборот: участники не судят), `/app/*` (управляет только организатор) |
| Судья (`judge`) | Только панель судьи `/judge/$slug`: свои назначения, оценки, самоотвод | Лидерборд закрыт слепотой (`judge_blind`) до финализации; кабинет команды закрыт (конфликт интересов — нельзя одновременно судить и участвовать); консоль организатора закрыта |
| Организатор (`organizer`) | Публичный лидерборд + консоль `/app/hackathons/$id` (все вкладки) + создание хакатонов | Кабинет команды закрыт (у организатора нет команды по определению — иначе стал бы участником собственного хакатона); панель судьи закрыта (оценки ставят назначенные судьи, при желании — отдельным аккаунтом) |

Правила, которых придерживается интерфейс:

- Ссылки в шапке лидерборда и на главной — только на своё рабочее место:
  кабинет команды показывается лишь участнику/зрителю/гостю, панель судьи — лишь
  судье, панель организатора — лишь организатору.
- Прямой ввод чужого URL не даёт «тихой» пустой страницы: везде показано, чья это
  зона, кем вы в этом хакатоне являетесь и куда перейти (кнопки-ссылки).
- Пустое состояние у судьи («назначений пока нет») отделено от запрета
  («панель только для судей») и от сетевой ошибки — у каждого свой текст.
- После `finalized`: лидерборд открывается всем (включая судей), судейские оценки
  и сабмишены замораживаются, консоль организатора — только чтение.

> Техническая оговорка: бэкенд местами шире модели выше — `POST /teams`
> и `GET /my-team` требуют только авторизации (без проверки роли), а
> `PUT /submissions/{teamId}` дополнительно разрешён организатору как технический
> оверрайд. Фронтенд осознанно не предлагает эти пути (организатору — не создавать
> команду, судье — не участвовать), но серверная проверка остаётся последним рубежом:
> чужие сабмишены и судейские оценки без назначения отклоняются с `403`.

## Переменные окружения

| Переменная | Где | Default | Назначение |
|------------|-----|---------|------------|
| `DATABASE_URL` | api/worker/sim | `postgres://hackathon:hackathon@localhost:5433/hackathon?sslmode=disable` (в compose — через `db:5432`) | Postgres |
| `ADDR` | api | `:8080` | listen-адрес API |
| `APP_ENV` | api/worker/frontend | `dev` (`prod` в оверлее) | окружение (cookie/LAN-политики) |
| `COOKIE_SECURE` | api | `false` | secure-флаг cookie сессий |
| `RECALC_INTERVAL` | worker | `5m` | периодический пересчёт активных треков |
| `SIM_API_URL` / `SIM_TOKEN` / `SIM_INTERVAL` | simulator | — / required / `30s` | генератор демо-метрик |
| `API_URL` | frontend (SSR runtime) | — | адрес API для server-side fetch |
| `VITE_API_URL` | frontend (build-time) | `http://api:8080` | fallback для SSR-сборки |

## Структура репозитория

```
backend/               Go API, worker, scoring engine, simulator, loadtest, seed, migrate
  cmd/                 api | worker | migrate | seed | simulator | loadtest
  internal/            auth, config, db, server, httpx, rbac + домены:
                       hackathons, teams, tracks, metrics, judging, ingest,
                       leaderboard (handler+SSE hub), scoring (engine+runner+worker),
                       submissions, audit, versioning, e2e
  migrations/          0001_init.sql (таблицы, триггеры append-only, функции NOTIFY)
frontend/              TanStack Start app (SSR leaderboard, judge UI, team cabinet, organizer console)
  src/routes/          index, h.$slug, h.$slug.teams.$teamId, judge.$slug,
                       team.$slug, hackathons.new, app.hackathons.$id, login
  src/lib/             api, sse, tracks, roles, labels, types
deploy/                nginx/nginx.conf, github-actions-benchmark.yml
docker-compose.yml     db, api, worker, simulator (profile demo), frontend, proxy
docker-compose.prod.yml  оверлей для VPS 1 ГБ: лимиты, тюнинг PG, без наружного порта БД
Makefile               up | down | logs | migrate | seed | test | backend-lint
```
