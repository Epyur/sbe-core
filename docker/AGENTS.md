# AGENTS.md — docker/ (инфраструктура стека)

Общая инфраструктура Docker-стека на сервере `/opt/mailers/`. Здесь — рабочая копия.

| Файл | Что это |
|---|---|
| `docker-compose.yml` | Сервисы: `postgres` (БД mailers), `backend` (mailer-service), `auth-db` (БД auth), `auth-service`, `documents-db`+`documents`, `lab-db`+`lab`, `ekn-db`+`ekn`, `contacts-db`+`contacts`, `agent-db`+`agent`+`agent-mermaid`, `photo-db`+`photo`, `caddy` (443), `portainer` (127.0.0.1:9000). Сеть `internal` |
| `caddy/Caddyfile` | TLS reverse-proxy `epyur.fvds.ru`: `/api/ekn/*` → ekn, `/api/agent/*` → agent, `/api/contacts/*` → contacts, `/api/lab/*` → lab, `/api/documents/*` → documents, `/api/photo/*` → photo, `/api/*` → backend, `/auth/*`, `/apps/*`, `/health` → auth-service |
| `nftables.conf` | Файрвол (вместо сломанного UFW): 22/80/443/25 и пр. |
| `.env.example` | Шаблон переменных (заглушки). Реальный `.env` — только на сервере |

## Правила

- Реальные секреты (`*.env`) в вольт не кладём — только `.env.example`.
  Актуальные значения: сервер `/opt/mailers/.env` + [гайд.md](../../../гайд.md).
- После правки compose/Caddyfile: `docker compose config` (валидация) → `docker compose up -d`.
- Caddy: домен `epyur.fvds.ru`, LE-сертификаты; порт 80 занят панелью — только 443.
- Portainer: таймаут сессии; доступ только через SSH-туннель на 127.0.0.1:9000.

## История

- **2026-08-28 — photo-service (Фотобанк):** добавлены `photo-db` (postgres) +
  `photo` (Go), Caddy `/api/photo/*` → `photo:3000` (перед `/api/documents/*` и
  `/api/*`), `.env`: `PHOTO_POSTGRES_*`, `PHOTO_OWNER_EMAIL`, `PHOTO_SERVICE_SECRET`,
  `PHOTO_APP_USER`/`PHOTO_APP_PASSWORD`. auth-service: `seedApps` → seed приложения
  `photo`. Бакет `sbe-photo` создан через rclone (ключи из `.env`). На сервере:
  залиты compose/Caddyfile/seed.go, пересобраны `auth-service` + `photo`/`photo-db`,
  создан app-пользователь `photo_app` (NOSUPERUSER + GRANT на схему public),
  Caddy пересоздан (`--force-recreate`, Caddyfile живёт в `./caddy/Caddyfile`),
  E2E зелёный (2026-08-28). Бэкапы `docker-compose.yml.bak-photo-20260828`,
  `Caddyfile.bak-photo-20260828`. ВАЖНО: локальный compose синхронизирован с
  серверным (в т.ч. LAB_SMTP-блок lab-service от 2026-08-28).

- **2026-08-25 — динамический `/registry.json`:** маршрут перенаправлен со статики на
  `auth-service` (публичный `GET /registry.json` = база из `./www/registry.json`
  + добавления администратора из БД auth); в auth-service добавлен маунт
  `./www:/srv/www:ro`. Позволяет ЦУП добавлять плагины в реестр без правки файлов.

- **2026-08-25 — documents: SMTP для уведомлений об истечении срока.** В сервис
  `documents` добавлены `SMTP_HOST: host.docker.internal`, `SMTP_PORT: 25`,
  `SMTP_FROM: ${SMTP_FROM}` (общая с auth-service), `SMTP_SKIP_VERIFY: 1` и
  `extra_hosts: host.docker.internal:host-gateway` (как у auth-service — иначе
  `host.docker.internal` не резолвится в контейнере). Задеплоено, письмо-уведомление
  доставлено (E2E 2026-08-25).

- **2026-08-16:** базовый стек + TLS + S3-бэкап. Подробности — [process.md](../../process.md).
- **2026-08-17:** добавлены `auth-db` + `auth-service` (Этап 1), в Caddyfile добавлены
  маршруты `/auth/*`, `/apps/*`, `/health`; `depends_on` caddy расширен. Перед правками
  сделаны бэкапы `docker-compose.yml.bak`, `.env.bak`, `Caddyfile.bak` на сервере.
- **2026-08-17 — Этап 3 (mailer-service):** backend переведён на JWT+роли (`/api/mailer/*`).
  compose: убран `INIT_TOKEN`, добавлены `JWT_SECRET`/`MAILER_APP_ID`/`MAILER_APP_NAME`/
  `MAILER_OWNER_EMAIL`/`MAILER_SERVICE_SECRET`/`AUTH_SERVICE_URL`, `depends_on` auth-service
  (`service_started`). `.env.example`: `API_TOKEN` убран. Caddyfile не менялся (`/api/*` уже
  покрывает `/api/mailer/*`). Бэкапы `docker-compose.yml.bak3`, `backend/main.go.bak3`.
- **2026-08-17 — шаблон письма:** backend получает mount `./templates:/app/templates:ro`
  и env `MAILER_TEMPLATE_DIR=/app/templates/standard.docx` (endpoint `/api/mailer/template`).
  На сервере создан `/opt/mailers/templates/` (файл + копия в S3). Бэкап `docker-compose.yml.bak4`.
- **2026-08-17 — documents-service (Документы):** добавлены `documents-db` (postgres) +
  `documents` (Go), Caddy `/api/documents/*` → `documents:3000` (перед `/api/*`), mount
  `./www:/srv/www:ro` (статик: `registry.json` по `https://epyur.fvds.ru/registry.json` —
  реестр ЦУП, чтобы не зависеть от rate-limit raw.githubusercontent.com 429).
  `.env`: `DOCUMENTS_*`, `S3_*`. Бэкапы `docker-compose.yml.bak9`, `Caddyfile.bak9`.
- **2026-08-17 — диск:** docker build cache разросся до ~4.3 ГБ от частых пересборок —
  очищен `docker builder prune -f` (диск 9.0 → 5.1 ГБ).
- **2026-08-18 — lab-service (Заявки на испытания):** добавлены `lab-db` (postgres) +
  `lab` (Go), Caddy `/api/lab/*` → `lab:3000` (до `/api/documents/*` и `/api/*`),
  `.env`: `LAB_POSTGRES_*`, `LAB_OWNER_EMAIL`, `LAB_SERVICE_SECRET`.
  auth-service: `seedApps` → seed приложения `lab` (LAB_APP_ID/NAME/OWNER_EMAIL/SERVICE_SECRET).
  На сервере: залиты compose/Caddyfile/seed.go, пересобраны `auth-service` + `lab`/`lab-db`,
  контейнеры запущены, E2E пройден (2026-08-18). Бэкапы `docker-compose.yml.bak11`,
  `Caddyfile.bak`, `.env.bak11`.
- **2026-08-20 — contacts-service (Контакты):** добавлены `contacts-db` (postgres) +
  `contacts` (Go), Caddy `/api/contacts/*` → `contacts:3000` (перед `/api/lab/*` и
  `/api/*`), `.env`: `CONTACTS_POSTGRES_*`, `CONTACTS_OWNER_EMAIL`, `CONTACTS_SERVICE_SECRET`.
  auth-service: `seedApps` → seed приложения `contacts`. На сервере: залиты
  compose/Caddyfile/seed.go, пересобраны `auth-service` + `contacts`/`contacts-db`,
  Caddy пересоздан `--force-recreate` (просто `up -d` не подхватывал новый Caddyfile),
  E2E пройден (2026-08-20). Бэкапы не делались (аддитивные правки).
- **2026-08-20 — agent-service (LogicTEAM.007):** добавлены `agent-db` (postgres) +
  `agent` (Go), Caddy `/api/agent/*` → `agent:3000` (перед `/api/contacts/*`), `.env`:
  `AGENT_POSTGRES_*`, `AGENT_OWNER_EMAIL`, `AGENT_SERVICE_SECRET`, `AGENT_S3_BUCKET=sbe-agent`.
  auth-service: `seedApps` → seed приложения `agent`. На сервере: залиты compose/Caddyfile/
  seed.go, пересобраны `auth-service` + `agent`/`agent-db`, Caddy пересоздан, E2E 16/16
  (2026-08-20). Бакет `sbe-agent` создан, cron очистки `0 4 * * *` (`--min-age 48h`).
- **2026-08-20 — контейнер `agent-mermaid` (рендер mermaid):** Node + `@mermaid-js/mermaid-cli`
  + системный chromium (node:20-slim, `PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium`,
  `PUPPETEER_SKIP_DOWNLOAD=true`), внутренний `POST /render`. `agent` получает
  `MERMAID_SERVICE_URL=http://agent-mermaid:3000`, `depends_on` agent-mermaid. Внешних
  роутов нет (внутренняя сеть). Пересобраны `agent-mermaid` + `agent`, E2E 14/14.
- **2026-08-20 — ограничение конкурентности:** `mem_limit` agent=512m, agent-mermaid=1g;
  в `agent` семафор 4 на generate/parse, в `agent-mermaid` лимит 2 рендера — очередь без
  ошибок (проверено 6 одновременных генераций → все 200).
