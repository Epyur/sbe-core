# specification.md — sbe-core (SBE Plugin System core)

## 1. Назначение

Source-only пакет (не плагин). Встраивается в каждый SBE-плагин при сборке;
`tokens.css`+`components.css` склеиваются в `styles.css` плагина через `build.onEnd`.
Никогда не публиковать standalone.

## 2. Мост `window.SBE`

Глобальное пространство `window.SBE = { services: Record<id, {api, meta}> }`.

| Функция | Назначение |
|---|---|
| `publishService(id, api, {version, name})` | Сервисный плагин публикует API в `onload` |
| `unpublishService(id)` | Снятие API в `onunload` |
| `getServiceSync(id)` | Синхронный доступ (null, если не опубликован) |
| `getService(id, timeoutMs=15000)` | Асинхронное получение: поллинг 200 мс; при таймауте — `Error('Сервис «{id}» недоступен. Установите и включите плагин «{имя}» из SBE Apstore')` |

Идентификаторы услуг — ключи `SbeServiceMap`:
`'sbe-apstore'`, `'sbe-llm'`, `'sbe-yougile'` (фаза 2, пока не реализован).

## 3. Реестр

- `DEFAULT_REGISTRY_URL = https://raw.githubusercontent.com/Epyur/sbe-apstore-registry/main/registry.json`.
- Схема `RegistryFile`: `{ schemaVersion, updatedAt, plugins: RegistryPluginEntry[] }`.
- `RegistryPluginEntry`: `{ id, dir, name, repo, branch?, required?, categories? }`.
  `id` — manifest id (для disablePlugin/enablePlugin), `dir` — папка установки
  (может отличаться от id — легаси `obsidian-yougile`/`yougile-tntn`).
- Текущий реестр (2026-08-14): `sbe-apstore` (required), `obsidian-yougile`,
  `sbe-llm` (required), `sbe-presentations`.

## 4. API-поверхности сервисов

- `SbeApstoreApi` — управление плагинами (см. `sbe-apstore/specification.md`), включая подсервис
  `auth: SbeAuthApi` и `announceUpdate({appId, appName, version, summary}): Promise<void>`
  (публикация новости об обновлении в общий канал, `visibility:'all', mandatory:false`).
- `SbeAuthApi` (подсервис `SbeApstoreApi.auth`) — ключ/JWT (`requestKey`/`activateKey`/`getToken`/
  `listDevices`/`revokeDevice`) + присутствие и новости: `getPresence(): Promise<PresenceInfo>`,
  `listNews(): Promise<NewsItem[]>`, `createNews(input): Promise<{id}>` (сервер: `restricted`/
  `mandatory` — только admin), `ackNews(id)`, `getNewsReads(id)` (только admin). См.
  `sbe-apstore/specification.md` разд. 3 и `server_back/auth-service/AGENTS.md`.
- `SbeLlmApi`:
  - `getStatus(): { configured: boolean; apiUrl: string }`
  - `complete(system, user, opts?: { model?, temperature? }): Promise<string>`
  - `completeJson<T>(system, user, opts?): Promise<T>`
  - `ask(question, opts?: { system?, context?, history?: {role:'user'|'assistant',text}[] , model? }): Promise<string>`
  - Центр НЕ знает моделей/промтов/контекста — всё передаёт потребитель.
- `SbeYougileApi` — зарезервирован (фаза 2): `getStatus()`, `authenticate()`, `client` (проекты/доски/колонки/пользователи/задачи/чаты/upload).

## 5. Установщик (`installer.ts`)

Скачивание файлов плагина: `GET https://raw.githubusercontent.com/{repo}/{branch}/{manifest.json|main.js|styles.css}`
через `requestUrl` → запись vault adapter → `delete require.cache` → `disablePlugin(id)`/`enablePlugin(id)`.

## 6. Ошибки

- `errorMessage(e: unknown)` — единое извлечение текста ошибки в `catch` (Error.message / String(e)).
- Мост: таймаут получения услуги → понятная ошибка с именем плагина.

## 7. Проверка

- `npx tsc --noEmit` (EXIT=0). Styles проверяются сборкой любого SBE-плагина.