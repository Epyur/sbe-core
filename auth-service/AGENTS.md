# AGENTS.md — auth-service

Серверный Go-сервис авторизации («паспортный стол») для SBE-плагинов.
Контейнер `auth-service` + отдельная БД `auth` (postgres `auth-db`).
Деплой на VDS: копия этой папки в рабочую папку Docker-стека (путь — вне git, см. рабочую документацию).

## Назначение

- Ключ на пару `email + device_id`, доставка через exim (SMTP localhost:25).
- JWT HS256 (общий `JWT_SECRET`) для plugin-services, TTL 1 ч.
- Таблицы: `users`, `devices`, `keys`, `apps` (реестр plugin-services + сервисные секреты).
- Ограничение домена email: `tn.ru` (env `ALLOWED_EMAIL_DOMAIN`).

## Эндпоинты

| Метод | Путь | Описание |
|---|---|---|
| GET | `/health` | статус + ping БД |
| POST | `/auth/request-key` | `{email, device_id}` → ключ по SMTP |
| POST | `/auth/activate-key` | `{email, device_id, key}` → статус `active` |
| POST | `/auth/token` | `{key, app_id}` → JWT (app_id должен быть в `apps`) |
| GET | `/auth/devices` | список устройств (Bearer <key>) |
| DELETE | `/auth/devices/{device_id}` | отзыв устройства (Bearer <key>) |
| GET | `/auth/presence` | кто онлайн (last_seen ≤30 мин) + всем пользователям last-seen для admin (Bearer <key>) |
| POST | `/auth/news` | публикация новости; `visibility:'all', mandatory:false` — любой авторизованный, `restricted`/`mandatory` — только admin (Bearer <key>) |
| GET | `/auth/news` | сообщения, видимые вызывающему, с флагом `read` (Bearer <key>) |
| POST | `/auth/news/{id}/ack` | отметить прочитанным (Bearer <key>) |
| GET | `/auth/news/{id}/reads` | кто из адресатов прочитал — только admin (Bearer <key>) |
| POST | `/auth/feedback` | обратная связь `{plugin_id, text}` (Bearer <key>): письмо владельцу плагина (ownerEmail из реестра) или, при пустом `plugin_id` («идея»), собственнику ЦУП (первый `ADMIN_EMAILS`); журнал `feedback_messages` |
| POST | `/apps/register` | регистрация plugin-service |

## Конфиг (env)

`DATABASE_URL`, `JWT_SECRET`, `ALLOWED_EMAIL_DOMAIN`, `SMTP_HOST`/`SMTP_PORT`/`SMTP_FROM`/`SMTP_USER`/`SMTP_PASS`/`SMTP_SKIP_VERIFY`,
`MAILER_APP_ID`/`MAILER_APP_NAME`/`MAILER_OWNER_EMAIL`/`MAILER_SERVICE_SECRET` (seed apps), `AUTH_TOKEN_TTL`,
`APPS_REGISTER_SECRET` (мастер-секрет регистрации приложений), `RATE_LIMIT_PER_10MIN` (по умолч. 3), `MAX_PENDING_KEYS` (по умолч. 5),
`ADMIN_EMAILS` (через запятую — кто видит `all_users` в `/auth/presence` и может слать `restricted`/`mandatory` новости).

## Сборка / проверка

```
docker compose up -d --build auth-service        # на сервере
docker compose exec auth-service wget -qO- http://localhost:3000/health
```

## История

- **2026-09-02 — регистрация приложения `llm` (`seed.go`).** Новый сервис
  `sbe-llm/llm-service` (хранение ключа API провайдера ИИ, привязанного к email
  пользователя, см. `sbe-llm/llm-service/AGENTS.md`) зарегистрирован тем же
  `seedApp()`, что и остальные (`photo`/`lab`/`ekn`/`contacts`/`agent`) — env
  `LLM_APP_ID`/`LLM_APP_NAME`/`LLM_OWNER_EMAIL`/`LLM_SERVICE_SECRET`. Никакой
  модели ролей у `llm` нет — валидного JWT с `app_id=llm` достаточно (все операции
  скопированы на email из токена). Развёрнуто, `docker compose up -d --build
  auth-service`, apps-таблица подтверждена.

- **2026-09-02 — вход по magic-link для «ЦУП Веб» (веб-портал sbe-photobank+
  sbe-requests, `web/sbe-web/`).** Не новая модель авторизации, а альтернативная
  доставка уже существующего «ключа» (`users`/`devices`/`keys`) — по одноразовой
  ссылке из письма вместо кода, вводимого вручную (request-key/activate-key).
  - Новая таблица `magic_links` (`token_hash` PK, `device_id`, `status`,
    `expires_at`, `consumed_at`); `devices.channel` (`'plugin'` default | `'web'`) —
    читается downstream (photo-service/lab-service) из нового JWT-claim `channel`
    для урезания прав веб-сессий (запрет записи в Фотобанке, клэмп
    `superadmin`→`admin` в Заявках).
  - `POST /auth/web/request-link` (магазин `email`, домен `tn.ru`, тот же
    rate-limit, что у `request-key`) → генерирует `device_id` сам (у браузера
    своего нет, в отличие от плагина), магический токен (`newKey()`, хранится
    только хэш), письмо через `sendMagicLinkEmail` (обёртка над `sendMail`).
  - `POST /auth/web/consume` (`consumeLim`, тот же паттерн, что
    `activateLim`/`tokenLim`) — проверка токена (не просрочен, `status='pending'`),
    помечает `consumed` (одноразовость), выдаёт сразу активный ключ (владение
    email уже подтверждено переходом по ссылке — отдельного шага активации, в
    отличие от `request-key`, не нужно).
  - `signJWT`/`POST /auth/token` — новый параметр/поле `channel` в `jwt.MapClaims`
    (значение из `devices.channel`), `handleServiceToken` — `channel="service"`
    (межсервисные вызовы lab→ekn, не сессия конечного пользователя).
  - `go build`/`go vet`/`go test` — чисто. Задеплоено, E2E пройден живьём
    (реальная доставка письма, `250 Ok` от mx1.tn.ru, consume/повторный
    consume/token с проверкой claim `channel` — все тестовые устройства и
    ключи после проверки удалены из БД).
- **2026-08-29 — `allowedAppEnvKeys["lab"]` += `LAB_MAIL_DEFAULT_PROJECT_CODE`.**
  Прямое продолжение фичи lab-service (проект по умолчанию для заявок из
  письма без ЕКН, см. `lab-service/AGENTS.md`/`sbe-lims/AGENTS.md`) — новый
  admin-управляемый ключ (не секрет, тот же канал, что остальные `LAB_MAIL_*`).
  `TestAllowedAppEnvKeysLabWhitelist` обновлён (было 6 ключей, стало 7).
  `go build`/`go vet`/`go test` — чисто. Задеплоено (`docker compose up -d
  --build auth-service`), контрольная сумма файла совпала, health ok.
- **2026-08-28 — seed приложения `photo`:** `seedApps` дополнен `PHOTO_*` (app_id
  `photo`, owner polishchuk@tn.ru, PHOTO_SERVICE_SECRET). На сервере пересобран
  `auth-service`; приложение `photo` зарегистрировано (см. photo-service/AGENTS.md,
  sbe-photobank Этап 2).
- **2026-08-28 — Обратная связь (`POST /auth/feedback`).** Авторизованный
  пользователь (Bearer <мастер-ключ>, `requireKey`) оставляет обращение
  `{plugin_id, text}`. Получатель определяется по реестру: для известного
  плагина — его `ownerEmail` (база `/srv/www/registry.json` + добавления
  `registry_additions`), при отсутствии ownerEmail — первый `ADMIN_EMAILS`;
  пустой `plugin_id` («Есть идея») — первый `ADMIN_EMAILS` (собственник ЦУП).
  Письмо уходит через общий `sendMail` (email.go рефакторинг: из `sendKeyEmail`
  вынесен универсальный `sendMail(to, subject, body)`). Журнал `feedback_messages`
  (автор, плагин, получатель, текст, status `sent`/`failed`) — обращение не
  теряется при сбое SMTP. Валидации: текст обязателен и ≤ 4000 симв., неизвестный
  плагин → 400. Клиент — `sbe-apstore` 0.3.10 (`auth.sendFeedback`). `go build`/
  `go vet`/`go test` — чисто. **Задеплоено на VDS** (`docker compose up -d
  --build auth-service`), контрольные суммы файлов совпали с локальными, health
  ok, таблица `feedback_messages` создана миграцией, роут `POST /auth/feedback`
  зарегистрирован (без ключа → 401). Полный E2E (отправка с реальным ключом
  → письмо владельцу) — при следующей ручной проверке из ЦУП.

- **2026-08-26 — Generic admin-канал для произвольных env-переменных приложений
  (`app_env_pending`), первый потребитель — `LAB_MAIL_*` (учётка почты
  email-приёма lab-service).** Продолжение записи 2026-08-25 ниже —
  `secret_rotations` жёстко привязана к одному секрету `{APP}_SERVICE_SECRET`
  на приложение (PK по `app_id`, значение генерируется сервером); для
  admin-ЗАДАННЫХ значений произвольных ключей (пароль почты, IMAP-сервер и
  т.п. — план `docs/superpowers/plans/2026-08-25-sbe-secrets-cup-plan.md`,
  раздел A1) понадобился отдельный механизм, не переиспользующий эту таблицу
  (иначе смешались бы два разных по семантике потока).
  - Новая таблица `app_env_pending` (`migrate.go`): PK `(app_id, env_key)` —
    несколько независимых ключей на приложение одновременно (в отличие от
    `secret_rotations`). `value` **обнуляется сразу после apply** (строже, чем
    `secret_rotations.new_secret`, который остаётся в таблице навсегда, — этот
    секрет нужен только на один цикл применения, хранить его в БД постоянно
    незачем).
  - `env_admin.go` (новый файл): `GET/POST /auth/apps/env` (admin, тот же
    мастер-ключ устройства, что `secret_admin.go`). **Критично** —
    `allowedAppEnvKeys` — явный белый список разрешённых ключей НА КАЖДОЕ
    приложение (сейчас только `lab`: 6 `LAB_MAIL_*`); без него этот эндпоинт
    стал бы способом переписать ЛЮБУЮ переменную сервера (`JWT_SECRET`,
    `DATABASE_URL` чужого сервиса) одним admin-запросом — весь смысл ревью
    безопасности свёлся бы к дыре того же класса. `POST` отклоняет ВЕСЬ запрос
    целиком при первом неразрешённом ключе (не применяет частично).
    `isValidEnvValue` — отвергает `\r`/`\n`/`\x00` (перевод строки в значении
    сломал бы построчный парсинг хост-скрипта — `secret-applier.sh` читает
    `psql`-вывод по одной строке на пару ключ-значение) и значения длиннее 4096.
  - `secret-applier.sh` (хост-скрипт на сервере, НЕ в git — правится
    напрямую по ssh) расширен вторым циклом: несколько pending-строк одного
    `app_id` группируются, чтобы пересоздать контейнер ОДИН раз (не на каждый
    ключ); запись в `.env` — через `awk -v` (НЕ `sed`), т.к. значение (пароль
    почты) может содержать любые символы — `sed`-замена с ним небезопасна
    (спецсимволы `&`/`\`/разделитель ломают подстановку или создают
    неожиданный результат).
  - `types.ts`/`sbe-apstore`: мост `getAppEnvStatus`/`setAppEnv` — см.
    `sbe-apstore/AGENTS.md` (v0.3.7). Клиент — `sbe-lims/src/ui/settings-tab.ts`
    (раздел «Приём результатов по email»), см. `sbe-lims/AGENTS.md` (v0.2.10).
  - Тесты (первые в этом репозитории — раньше их не было вовсе):
    `env_admin_test.go` — `TestIsValidEnvValue` (граница длины, control-символы),
    `TestAllowedAppEnvKeysLabWhitelist` (регресс на белый список + явная
    проверка, что опасные системные переменные ни в один whitelist не попали).
  - `go build`/`go vet`/`go test` — чисто. Задеплоено на VDS
    (`docker compose up -d --build auth-service`), миграция подтверждена
    (`\d app_env_pending`). **E2E пройден на реальном ключе** (не тестовые
    данные — `LAB_MAIL_POLL_INTERVAL_SECONDS=120`, безопасное значение):
    прямая вставка в `app_env_pending` → ручной запуск `secret-applier.sh` →
    значение появилось в `.env` (66 строк файла, счётчики `LAB_MAIL_*`/
    `LAB_POSTGRES_*`/`JWT_SECRET` не изменились — awk-перезапись не повредила
    остальные строки), контейнер `lab` пересоздан, health ok, строка в БД —
    `status='applied'`, `value` пуст.

- **2026-08-25 — Управление секретами приложений + динамический реестр (ЦУП):**
  - `secret_admin.go`: admin-эндпоинты (мастер-ключ устройства + `ADMIN_EMAILS`)
    `GET/POST /auth/apps/secret` — статус / sync (`apps.service_secret` ← env
    `{APP}_SERVICE_SECRET`) / rotate (генерация нового 32-байтного hex + очередь в
    таблицу `secret_rotations`; применяет хост-скрипт `secret-applier.sh`
    по cron: обновляет `.env`, `apps` и пересоздаёт контейнер сервиса). Журнал
    `secret_audit`. В `apps` добавлена колонка `updated_at`.
  - `registry_admin.go`: динамический реестр плагинов — таблица `registry_additions`,
    admin-CRUD `GET/POST/DELETE /auth/registry`, публичный `GET /registry.json` =
    базовый `/srv/www/registry.json` (маунт `./www:/srv/www:ro`) + добавления.
    Caddy переключён со статики на `reverse_proxy auth-service:3000`.
  - E2E: rotate → applier применил (`.env`+`apps`+рестарт, `registerApp` OK);
    add → запись появилась в публичном `/registry.json`; delete → исчезла.
  - Деплой: `docker compose up -d --build auth-service`.

- **2026-08-17 — создание (Этап 1 плана 2026-08-16-sbe-server-auth-rights-plan.md).**
  Собрано в образ `mailers-auth-service`, контейнер `auth-service` + `auth-db` запущены.
  Миграции (`users`/`devices`/`keys`/`apps`) и seed приложения `mailer` выполняются при старте.
  Маршрутизация в Caddy: `/auth/*`, `/apps/*`, `/health` → auth-service; `/api/*` → backend.
- **2026-08-17 — фикс сборки:** убраны неиспользуемые импорты `pgxpool` из `migrate.go` и `seed.go`
  (первая сборка падала: "imported and not used"). После правки сборка зелёная.
- **2026-08-17 — SMTP через exim починен (3 правки):**
  1) `email.go` переписан с `smtp.SendMail` на явный `smtp.Client`: STARTTLS теперь с
     `SMTP_SKIP_VERIFY` (exim отдаёт самоподписанный сертификат без SAN → Go не мог проверить);
     добавлена поддержка `SMTP_USER`/`SMTP_PASS` (внешний релей). `docker-compose.yml` → `SMTP_SKIP_VERIFY: "1"`.
  2) На сервере: в `/etc/exim4/passwd` добавлен локальный отправитель
     `noreply@epyur.fvds.ru:!:65534:65534:/var/mail::0:` (шаблон ispmanager требует `verify = sender`).
  3) На сервере: в `/etc/exim4/relay_from_hosts` добавлено `172.16.0.0/12` (релей из docker-сети).
  Проверено E2E: `request-key` → письмо доставлено на `polishchuk@tn.ru` (mx1.tn.ru, 250 Ok).
  ⚠️ Правки exim — вручную на сервере, ispmanager может перезаписать при действиях в панели (см. weak-points.md A2/A3).
- **2026-08-17 — проверка эндпоинтов (полный цикл):**
  health, apps/register (upsert), request-key, activate-key (в т.ч. несоответствие device/email),
  token (в т.ч. неактивированный ключ, неизвестный app_id), list devices, отзыв устройства →
  после отзыва token выдаёт ошибку. Секрет приложения после теста `apps/register` восстановлен в БД.
- **2026-08-17 — hardening (A3/A4/A5/A9 из weak-points.md):**
  - **A4** — `apps/register` защищён: `authorizedRegister` (мастер-секрет `APPS_REGISTER_SECRET`
    из env ИЛИ совпадение с текущим `service_secret` записи), иначе 403. Новое приложение — только
    по мастер-секрету. Из `Caddyfile` удалён публичный `handle /apps/*` (регистрация — по внутренней сети).
    Проверено: без секрета 403, неверный 403, существующий секрет ok, мастер ok, публичный `/apps/*` → fallback.
  - **A5** — `requestLimitExceeded` в `request-key`: ≤ `RATE_LIMIT_PER_10MIN`=3 ключей за 10 мин на email
    + ≤ `MAX_PENDING_KEYS`=5 pending-ключей; превышение → 429. Проверено: req1–3 ok, req4 → 429.
  - **A3** — релей exim сужен с `172.16.0.0/12` до `172.18.0.0/16` (сеть `mailers_internal`);
    проверено `exim -bh 172.18.0.3` → `250 Accepted`. Бэкап `relay_from_hosts.bak2`.
  - **A9** — Portainer: учётка `epyur` (не admin), пароль утерян → сброшен через
    `portainer/helper-reset-password` на volume `mailers_portainer_data` (compose-префикс `mailers_`;
    см. weak-points.md A9), затем сменён через `PUT /api/users/1/passwd`, вход проверен.
    Порт 9000 — только 127.0.0.1 (bind + nft).
  - В `docker-compose.yml`/`.env` добавлен `APPS_REGISTER_SECRET`; в `.env.example` — плейсхолдер.
  - Тестовые данные (устройства `aaaaaaaa-…`, app `newapp`) после проверок удалены из БД.
  ⚠️ При добавлении новых compose-сетей — дополнить `relay_from_hosts` (см. weak-points.md A3).
- **2026-08-18 — seed приложения `lab`:** `seedApps` дополнен `LAB_*` (app_id `lab`, owner
  polishchuk@tn.ru, LAB_SERVICE_SECRET). На сервере пересобран `auth-service` (без этого
  `/apps/register` для lab отвечал 403). Приложение `lab` зарегистрировано (см. lab-service/AGENTS.md).
- **2026-08-20 — seed приложения `contacts`:** `seedApps` дополнен `CONTACTS_*` (app_id
  `contacts`, owner polishchuk@tn.ru, CONTACTS_SERVICE_SECRET). На сервере пересобран
  `auth-service`; приложение `contacts` зарегистрировано (см. contacts-service/AGENTS.md).
- **2026-08-20 — seed приложения `agent`:** `seedApps` дополнен `AGENT_*` (app_id `agent`,
  owner polishchuk@tn.ru, AGENT_SERVICE_SECRET). На сервере пересобран `auth-service`;
  приложение `agent` зарегистрировано (см. agent-service/AGENTS.md).
- **2026-08-22 — присутствие + канал «Новости» (для ЦУП/sbe-apstore):**
  - Миграция: `devices.last_seen_at TIMESTAMPTZ`; таблицы `news_messages`/`news_recipients`/`news_reads`.
  - `POST /auth/token` теперь пишет `last_seen_at = now()` для устройства сразу после успешной
    валидации ключа — единая точка учёта активности для всех plugin-services (каждый сперва
    получает здесь токен).
  - Новое понятие «администратор»: `ADMIN_EMAILS` (env, через запятую) → `parseAdminEmails()`/`isAdmin()`.
  - Новые роуты (все через существующий `requireKey`, Bearer <мастер-ключ устройства>):
    `GET /auth/presence` (кто online за 30 мин + `all_users` c last-seen для admin),
    `POST /auth/news`, `GET /auth/news`, `POST /auth/news/{id}/ack`, `GET /auth/news/{id}/reads` (admin).
  - Решение по правам на `POST /auth/news`, отступающее от первоначального плана «только admin»:
    общедоступная необязательная новость (`visibility:'all', mandatory:false`) разрешена ЛЮБОМУ
    авторизованному пользователю — это нужно, чтобы `announceUpdate` любого SBE-плагина работал не
    только с админского устройства; `restricted` или `mandatory` — только admin (403 иначе).
  - `go build`/`go vet`/`go test` — чисто. Задеплоено на VDS: `ADMIN_EMAILS=polishchuk@tn.ru`
    в `.env` (как и все остальные `*_OWNER_EMAIL`), `docker-compose.yml` обновлён,
    `docker compose up -d --build auth-service`, health OK.
  - **Фикс после первого деплоя**: `/auth/presence`/`/auth/devices`/`/auth/news*` авторизуются
    мастер-ключом устройства через `requireKey` — **минуя** `/auth/token`, поэтому открытие
    «Онлайн»/«Новости» само по себе не обновляло `last_seen_at` (пользователь не видел себя
    online, admin-таблица показывала «никогда» даже для только что активного устройства).
    Добавлен `touchLastSeen(ctx, deviceID)`, вызывается и из `handleToken`, и из `requireKey` —
    теперь ЛЮБОЙ авторизованный запрос к auth-service (не только выдача JWT) считается
    активностью. Проверено на живых данных: `SELECT last_seen_at FROM devices` — было `NULL`,
    после повторного клика по «Онлайн» — актуальная метка времени. Передеплоено, health OK.

## Статистика ошибок и отступлений

- Правило проекта: импорты без неиспользуемых. Замечаний на текущий момент нет (после фикса 2026-08-17).
- Известные ограничения: `smtp.PlainAuth` не используется без `SMTP_USER` (локальный exim без аутентификации).
