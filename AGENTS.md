# AGENTS.md — sbe-core (SBE Plugin System core)

Source-only пакет системы SBE: общие типы, мост, клиент реестра, установщик и
design-токены. **Не плагин** (нет `manifest.json`; Obsidian его игнорирует).
Встраивается в каждый SBE-плагин при сборке через относительные импорты
`../../sbe-core/src/...`; styles склеиваются в `styles.css` плагина через
`build.onEnd` в его esbuild-конфиге. Никогда не публиковать как standalone-плагин.

**Git-репозиторий заведён 2026-08-24** (`Epyur/sbe-core`, публичный) — раньше эта папка
была без git вовсе. **Бэк общей инфраструктуры** — `auth-service/` (авторизация для
ВСЕХ приложений) + `docker/` (docker-compose/Caddy/nftables стека) + `weak-points.md` —
на отдельной ветке `backend` (main — чистый релизный срез исходников sbe-core, без
бэка). Ни auth-service, ни docker не привязаны к одному плагину-потребителю — отсюда
их дом здесь, а не в конкретном `sbe-*`. См. правило «Бэки в папках плагинов» в
корневом `plugins/AGENTS.md` и указатель в `../server_back/AGENTS.md`.

## Структура

- `src/types.ts` — реестр (`RegistryPluginEntry`, `RegistryFile`), `SbeApstoreApi`, `SbeLlmApi`, `SbeYougileApi`, `SbeServiceMap` (типизированный словарь сервисов).
- `src/bridge.ts` — мост `window.SBE`: `publishService`/`unpublishService`/`getServiceSync`/`getService` (поллинг 200 мс, таймаут 15 с, понятная ошибка с именем плагина).
- `src/registry.ts` — `DEFAULT_REGISTRY_URL` (`raw.githubusercontent.com/Epyur/sbe-apstore-registry/main/registry.json`) + типы.
- `src/installer.ts` — скачивание файлов плагина с `main`-ветки (`rawUrl()` →
  `raw.githubusercontent.com/<repo>/<branch>/<file>`), запись адаптером,
  `require.cache` очистка, SHA-256 по хешам реестра; опция `skipReload`
  (самообновление установщика). **⚠️ При обновлении `hashes` в
  `sbe-apstore-registry/registry.json` после релиза ЛЮБОГО SBE-плагина —
  обязательный порядок с retry-проверкой против `raw.githubusercontent.com`
  (не одна проверка, CDN этого домена пропагируется с задержкой) — см.
  `sbe-apstore-registry/AGENTS.md`, было 2 живых инцидента с «Контрольная
  сумма не совпадает» на проде из-за нарушения этого порядка.**
- `src/auth-client.ts` — серверный auth-клиент `AuthService` (ключ на email+device_id, JWT, устройства, новости, presence, env, динамический реестр) — общий для `sbe-apstore` и `sbe-mobile` (перенесён 2026-08-26).
- `src/store-manager.ts` — состояние магазина `StoreManager` (реестр, карточки, установка/обновление) — общий для `sbe-apstore` и `sbe-mobile` (перенесён 2026-08-26).
- `src/utils/errors.ts` — `errorMessage(e: unknown)` для всех `catch`.
- `src/design/tokens.css` + `components.css` — дизайн-система SBE (`tn-*`).

## Ключевые решения

- Мост: глобальный `window.SBE`, услуги публикуются при `onload` плагина, снимаются в `onunload`. Порядок загрузки плагинов не важен — `getService` поллит.
- `getService` бросает `Сервис «{id}» недоступен. Установите и включите плагин «{имя}» из SBE Apstore` (имена в `getServiceName()`).
- `SbeLlmApi` — центр хранит только `apiUrl` + ключ; `getStatus(): {configured, apiUrl}`, без `resolveModel`/моделей. Модель и промты — у потребителя.

## История работ

### 2026-08-29 — перенос из backend: клиентская часть инструмента ручной загрузки файлов

`pluginFileUrl()` в `registry.ts` (GitHub raw vs `epyur.fvds.ru/plugins/*` по
`entry.selfHosted`), `RegistryPluginEntry` += `selfHosted?`/`uploadedBy?`/
`uploadedAt?`, `auth-client.ts` += `uploadRegistryFiles()`. Полная история
(бэкенд/хранение/UI) — `AGENTS.md`/`docs/superpowers/specs/
2026-08-29-sbe-plugin-file-upload-design.md` ветки `backend`.

### 2026-08-28 — RegistryPluginEntry.appId (динамический белый список токенов)
- В `RegistryPluginEntry` добавлено поле `appId?: string` — маркер «у плагина есть
  серверная часть». ЦУП (sbe-apstore 0.3.12) строит из таких записей динамический
  белый список выдачи токенов `getToken` (вместо хардкода из v0.3.8). Аддитивно;
  существующие плагины не пересобираются.

### 2026-08-28 — обратная связь в SbeAuthApi (sendFeedback)
- `SbeAuthApi`: новый метод `sendFeedback(input: SendFeedbackInput)` (эндпоинт
  `POST /auth/feedback` в auth-service, см. `auth-service/AGENTS.md`) + тип
  `SendFeedbackInput` {pluginId, text}. Замечание уходит владельцу плагина
  (ownerEmail из реестра), пустой pluginId («идея») — собственнику ЦУП.
- Реализация в `auth-client.ts` (Bearer <мастер-ключ>). Аддитивно; потребитель —
  sbe-apstore (0.3.10, форма «Обратная связь»). Другие плагины не пересобирались.

### 2026-08-27 — SbeDashboardsApi + listCharts
- Добавлены `SbeDashboardsApi extends SbeOpenableApi` (метод `listCharts(): Promise<
  DashboardChartMeta[]>`, тип `DashboardChartMeta` {id, slug, title, source,
  description, updatedAt}), `'sbe-dashboards'` в `SbeServiceMap`, `getServiceName`
  → «LogicTEAM.Дашборды». Аддитивно; существующие плагины не пересобираются.
- Создан плагин `sbe-dashboards` (v0.1.0 → 0.1.1); sbe-presentations 0.3.9 забирает
  список графиков через `getService('sbe-dashboards').listCharts()`.

### 2026-08-26 — общий клиент для мобильного хаба (auth-client.ts + store-manager.ts)
- `AuthService` (бывш. `sbe-apstore/src/services/auth-service.ts`) → `src/auth-client.ts`;
  `StoreManager` (бывш. `sbe-apstore/src/services/store-manager.ts`) → `src/store-manager.ts`.
  Общие для десктопного ЦУП (sbe-apstore 0.3.9) и нового мобильного хаба
  `sbe-mobile` (v0.1.0). Потребители пересобраны только те, кто их использует
  (политика 2026-08-20).
- `installer.ts`: опция `skipReload` — пропустить clearRequireCache + disable/enable
  (самообновление плагина-установщика; файлы пишутся, перезапуск инициирует вызывающий).

### 2026-08-25 — управление секретами приложений и динамический реестр (ЦУП)
- `SbeAuthApi`: новые методы + типы:
  - `manageAppSecret({appId, action: 'status'|'sync'|'rotate'})` → `ManageAppSecretResult`
    — статус/синхронизация/ротация `service_secret` приложения (только администратор);
  - `listRegistryAdditions()`, `addRegistryPlugin(plugin)`, `removeRegistryAddition(id)`
    + типы `RegistryAddition`, `RegistryPluginInput` — динамический реестр плагинов
    (админ добавляет плагин из ЦУП, он сразу появляется в `/registry.json`).
- Реализация на сервере — auth-service (ветка `backend`, см. его AGENTS.md); UI — ЦУП
  (sbe-apstore 0.3.6). Потребители не пересобирались, кроме использующих метод.

### 2026-08-22 — присутствие + новости в SbeAuthApi/SbeApstoreApi
- `SbeAuthApi`: новые методы `getPresence()`, `listNews()`, `createNews()`, `ackNews()`,
  `getNewsReads()` + типы `PresenceInfo`, `NewsItem`, `CreateNewsInput`, `NewsReadStatus`
  (соответствуют новым эндпоинтам `auth-service`, см. server_back/auth-service/AGENTS.md).
- `SbeApstoreApi`: новый метод `announceUpdate({appId, appName, version, summary})` — любой
  SBE-плагин публикует в канал «Новости» сообщение о своём обновлении (общий доступ, без
  авто-открытия) через `getService('sbe-apstore')`.
- Тип `AnnounceUpdateInput`.
- Реализация и версия — только в `sbe-apstore` (единственный потребитель/реализатор на
  данный момент); по политике ниже (2026-08-20) остальные плагины НЕ пересобирались/не
  бампались — изменение аддитивное, `announceUpdate` пока никем не вызывается (это отдельная,
  более крупная задача — правка `onload()` каждого плагина).

### 2026-08-20 — SbeContactsApi; политика пересборки изменена
- Добавлены `SbeContactsApi` (extends `SbeOpenableApi`), `'sbe-contacts'` в `SbeServiceMap`,
  `getServiceName` → «Контакты».
- Создан плагин `sbe-contacts` (v0.1.1). В этой сессии впервые пересобраны и подняты версии
  у 9 существующих плагинов — **впредь так НЕ делаем** (решение пользователя 2026-08-20):
  изменения sbe-core аддитивные, существующие плагины пересобираются/бампятся **только при
  изменениях по существу в самом плагине** (реальные баги, добавление/удаление функциональности).
  Исключение — новый плагин (нужен актуальный sbe-core при первой сборке).

### 2026-08-18 — SbeLimsApi (Этап 5-6 плана sbe-lims)
- Добавлены `SbeLimsApi` (extends `SbeOpenableApi`), `'sbe-lims'` в `SbeServiceMap`,
  `getServiceName` → «ЛИМС».
- Создан плагин `sbe-lims` (v0.1.0); пересобраны все SBE-плагины (2026-08-18, после этой
  правки: sbe-apstore/sbe-calendar/sbe-documents/sbe-ekn/sbe-lims/sbe-llm в 17:00-17:10,
  sbe-mailer/sbe-presentations/sbe-requests/sbe-tasks/sbe-yougile при сверке сборок позже).
  tsc/build — зелёные.
- Реестр `sbe-lims` добавлен и синхронизирован на сервер; `community-plugins.json` дополнен;
  git-репо `Epyur/sbe-lims` создано и запушено (2026-08-18).

### 2026-08-18 — SbeRequestsApi (Этап 2 плана sbe-requests)
- Добавлены `SbeRequestsApi` (extends `SbeOpenableApi`), `'sbe-requests'` в `SbeServiceMap`,
  `getServiceName` → «Заявки на испытания».
- Пересобраны все 8 SBE-плагинов (в т.ч. новый sbe-requests 0.1.0). tsc/build — зелёные.

### 2026-08-17 — Дизайн-токены выровнены со словарём TN Life UI kit (аддитивный рефактор)
- `src/design/tokens.css` перестроен по слоям (обратная совместимость сохранена):
  1) примитивы `--tn-*` (имена/значения не менялись) → 2) сырая палитра `--tn-palette-*`
  (ключевые ступени neutral/red/blue/green/orange из `uikit/variables.css`) → 3) семантические
  токены TN Life `--content-*`/`--background-*`/`--border-*` (точные имена Life) → 4) `.tn-dark-theme`
  (значения из `variables-dark.css`, opt-in).
- Правило для новых компонентов: использовать семантические токены (слой 3), не примитивы —
  перенос в Vue/Life станет механическим.
- Существующие плагины не затронуты: ни один `--tn-*`/класс не удалён/не переименован.
  Пересобраны потребители: sbe-apstore 0.3.0, sbe-tasks 0.1.4, sbe-calendar 0.1.2 (build ok).

### 2026-08-17 — SbeAuthApi (Этап 2 плана 2026-08-16-sbe-server-auth-rights-plan)
- `SbeApstoreApi` расширен подсервисом `auth: SbeAuthApi` (ключ на email+device_id,
  JWT для plugin-services). Новые типы: `SbeAuthApi`, `DeviceInfo`.
- В `RegistryPluginEntry` добавлено поле `ownerEmail` (источник первого админа
  plugin-service, используется на Этапе 3/4).
- Потребители с этим набором типов пересобраны: sbe-apstore 0.3.0. У sbe-core нет
  собственной версии (source-only).

### 2026-08-14 — v0.1.0 (создание)
- Создан по дизайну `docs/superpowers/specs/2026-08-14-sbe-plugin-system-design.md`.
- `npx tsc --noEmit` EXIT=0.
- **2026-08-14 (sbe-llm/sbe-presentations)**: `SbeLlmApi` переработан — убраны `resolveModel`, `getStatus().models/defaultModel`; добавлен `ask(question, {system?, context?, history?, model?})`. В `bridge.ts` сообщение об ошибке таймаута дополнено именем плагина (`getServiceName`). Потребители: `sbe-llm` (publish), `sbe-presentations` (getService).
- **Примечание конвенции**: с 2026-08-14 каждая папка плагина ведёт свой `AGENTS.md` (история) + `specification.md`. Этот файл создан задним числом по конвенции.
- **2026-08-15 (sbe-apstore v0.2.1)**: строка «SBE Apstore» в `bridge.ts` (ошибка таймаута, `getServiceName`) и `installer.ts` (Notice) заменена на «ЦУП СБЕ ПМиПИР». Пересобраны все 4 SBE-плагина.
- **2026-08-15 (sbe-tasks)**: `SbeYougileApi.client` расширен — `getColumns(boardId?)`, `getColumnById`, `getTaskChatSubscribers`; добавлен `SbeTasksApi` в `SbeServiceMap`. Пересобраны sbe-apstore/sbe-llm/sbe-presentations/sbe-yougile/sbe-tasks. Версии потребителей: sbe-apstore 0.2.2, sbe-llm 0.1.1, sbe-presentations 0.2.1, sbe-yougile 0.1.1. У sbe-core нет собственной версии (source-only, без manifest) и нет git-репозитория.

## Статистика ошибок и отступлений

- Нарушений нет: `window.setTimeout` в `bridge.ts` — корректно; `instanceof Error` в `utils/errors.ts` — реализация самого `errorMessage()`. 0 `any`, 0 `fetch`, 0 инлайн-стилей.

## Правила

- `catch(e: unknown)` + `errorMessage()`; `requestUrl()`; `window.setTimeout()`; без `any`; классы `tn-*`; UI на русском.