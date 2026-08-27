/** Общие типы системы SBE: реестр, манифесты, API-поверхности сервисов. */

export interface RegistryPluginEntry {
  /** Manifest id плагина (используется для disablePlugin/enablePlugin). */
  id: string;
  /** Папка установки внутри .obsidian/plugins/ (может отличаться от id — см. легаси yougile-tntn). */
  dir: string;
  /** Человекочитаемое имя. */
  name: string;
  /** GitHub-репозиторий в формате owner/repo. */
  repo: string;
  /** Ветка, из которой качаются файлы. По умолчанию main. */
  branch?: string;
  /** Системный плагин, обязательный для работы системы. */
  required?: boolean;
  /** У плагина есть открываемый UI (вьюха), вызываемый из магазина через API. */
  hasView?: boolean;
  /** Категории для будущей фильтрации в магазине. */
  categories?: string[];
  /** Email владельца продукта (первый admin плагина). Источник истины — реестр,
   *  используется plugin-service при bootstrap прав. */
  ownerEmail?: string;
  /** SHA-256 (hex, lowercase) файлов для контроля целостности при установке
   *  (ревью B4). Отсутствие = установка без проверки (с предупреждением). */
  hashes?: { manifest?: string; main?: string; styles?: string };
}

export interface RegistryFile {
  schemaVersion: number;
  updatedAt: string;
  plugins: RegistryPluginEntry[];
}

/** Манифест Obsidian-плагина. */
export interface RemoteManifest {
  id: string;
  name: string;
  version: string;
  minAppVersion?: string;
  description?: string;
  author?: string;
  isDesktopOnly?: boolean;
}

export type PluginState = 'not-installed' | 'installed' | 'update-available' | 'required';

export interface PluginCard {
  entry: RegistryPluginEntry;
  remote: RemoteManifest | null;
  local: RemoteManifest | null;
  state: PluginState;
}

export interface InstalledPlugin {
  id: string;
  dir: string;
  name: string;
  version: string;
  description?: string;
  /** У плагина есть открываемый UI (флаг hasView из реестра). */
  hasView?: boolean;
}

export interface UpdateSummary {
  checkedAt: number;
  updates: Array<{ id: string; name: string; from: string; to: string }>;
}

/** API плагина «ЦУП СБЕ ПМиПИР» (центр управления плагинами). */
export interface SbeApstoreApi {
  getRegistry(): Promise<RegistryPluginEntry[]>;
  getPluginState(id: string): PluginState;
  install(id: string): Promise<void>;
  update(id: string): Promise<void>;
  updateAll(): Promise<{ updated: string[]; failed: string[] }>;
  checkUpdates(): Promise<UpdateSummary>;
  listInstalled(): InstalledPlugin[];
  /** Доступ к серверу: ключ на пару email+device_id, JWT для plugin-services. */
  auth: SbeAuthApi;
  /** Публикует в канал «Новости» (общий доступ, без авто-открытия) сообщение
   *  о своём обновлении. Любой SBE-плагин может вызвать через getService('sbe-apstore'). */
  announceUpdate(input: AnnounceUpdateInput): Promise<void>;
}

export interface AnnounceUpdateInput {
  appId: string;
  appName: string;
  version: string;
  summary: string;
}

/** Устройство в реестре auth-service (список из GET /auth/devices). */
export interface DeviceInfo {
  deviceId: string;
  label: string;
  createdAt: string;
  keyStatus: string;
}

/** API серверной авторизации SBE (публикуется ЦУП в составе SbeApstoreApi).
 *  Ключ хранится в secretStorage (стабильный секрет `sbe-auth-key`);
 *  `deviceId` — UUID в data.json ЦУП. */
export interface SbeAuthApi {
  getStatus(): { authorized: boolean; email?: string };
  /** Шаг 2 потока авторизации: ключ приходит на email. */
  requestKey(email: string): Promise<void>;
  /** Шаг 4: активировать ключ из письма (сохраняется в secretStorage). */
  activateKey(key: string): Promise<void>;
  /** Шаг 5: JWT для plugin-service по app_id (кэшируется до истечения). */
  getToken(appId: string): Promise<string>;
  listDevices(): Promise<DeviceInfo[]>;
  revokeDevice(deviceId: string): Promise<void>;
  /** Кто сейчас онлайн (действие синхронизации за последние 30 минут);
   *  `allUsers` приходит только для администратора (ADMIN_EMAILS). */
  getPresence(): Promise<PresenceInfo>;
  listNews(): Promise<NewsItem[]>;
  /** Только для администратора — сервер отвечает 403 остальным. */
  createNews(input: CreateNewsInput): Promise<{ id: number }>;
  ackNews(id: number): Promise<void>;
  /** Только для администратора — кто из адресатов прочитал сообщение. */
  getNewsReads(id: number): Promise<NewsReadStatus[]>;
  /** Только для администратора — статус/синхронизация/ротация service_secret приложения. */
  manageAppSecret(input: ManageAppSecretInput): Promise<ManageAppSecretResult>;
  /** Только для администратора — статус admin-управляемых env-переменных приложения
   *  (белый список на сервере, напр. LAB_MAIL_* — учётка почты email-приёма lab-service). */
  getAppEnvStatus(appId: string): Promise<AppEnvStatus>;
  /** Только для администратора — поставить в очередь новые значения env-переменных
   *  приложения (применяет хост-скрипт, сервис перезапускается). Значения не
   *  логируются и не возвращаются обратно нигде — только "set"/"pending" в статусе. */
  setAppEnv(appId: string, values: Record<string, string>): Promise<{ appId: string; pending: boolean }>;
  /** Только для администратора — список добавленных в динамический реестр плагинов. */
  listRegistryAdditions(): Promise<RegistryAddition[]>;
  /** Только для администратор — добавить плагин в динамический реестр. */
  addRegistryPlugin(plugin: RegistryPluginInput): Promise<{ id: number }>;
  /** Только для администратор — удалить запись из динамического реестра. */
  removeRegistryAddition(registryId: number): Promise<void>;
}

/** Управление service_secret приложения (auth-service /auth/apps/secret, admin). */
export interface ManageAppSecretInput {
  appId: string;
  action: 'status' | 'sync' | 'rotate';
}

export interface ManageAppSecretResult {
  appId: string;
  /** Секрет задан в таблице apps. */
  set?: boolean;
  /** Последнее изменение (apply), ISO или null. */
  updatedAt?: string | null;
  /** Есть незавершённая ротация (pending). */
  pending?: boolean;
  pendingSince?: string | null;
  /** sync: apps выровнен по env сервера. */
  applied?: boolean;
  /** rotate: новое значение (показывается один раз). */
  newSecret?: string;
}

/** Статус admin-управляемых env-переменных приложения (auth-service /auth/apps/env, admin).
 *  Значения самих переменных сервер никогда не возвращает — только факт/дату. */
export interface AppEnvStatus {
  appId: string;
  keys: AppEnvKeyStatus[];
}

export interface AppEnvKeyStatus {
  key: string;
  /** Значение когда-либо применено (лежит в .env сервера). */
  set: boolean;
  /** Последнее применение, ISO или null. */
  updatedAt: string | null;
  /** Есть заявка, ожидающая применения хост-скриптом. */
  pending: boolean;
  pendingSince: string | null;
}

/** Управление динамическим реестром плагинов (auth-service /auth/registry, admin). */
export interface RegistryAddition {
  registryId: number;
  createdAt: string;
  plugin: RegistryPluginEntry;
}

export interface RegistryPluginInput {
  id: string;
  dir?: string;
  name: string;
  repo: string;
  branch?: string;
  required?: boolean;
  hasView?: boolean;
  categories?: string[];
  ownerEmail?: string;
  description?: string;
}

/** Присутствие устройств (GET /auth/presence, auth-service). */
export interface PresenceInfo {
  online: string[];
  isAdmin: boolean;
  /** Только когда isAdmin — email + последний визит по всем устройствам (null = никогда). */
  allUsers?: Array<{ email: string; lastSeenAt: string | null }>;
}

/** Сообщение канала «Новости» (auth-service, news_messages). */
export interface NewsItem {
  id: number;
  authorEmail: string;
  title: string;
  body: string;
  visibility: 'all' | 'restricted';
  mandatory: boolean;
  createdAt: string;
  /** Прочитано ли вызывающим (news_reads). */
  read: boolean;
}

export interface CreateNewsInput {
  title: string;
  body: string;
  visibility: 'all' | 'restricted';
  /** Обязателен и непуст при visibility='restricted'. */
  recipients?: string[];
  mandatory: boolean;
}

export interface NewsReadStatus {
  email: string;
  read: boolean;
  readAt?: string;
}

/** API центрального LLM-агента. Хранит только apiUrl и API-ключ (secretStorage).
 *  Модели, промты и контекст передаёт потребитель — центр о них ничего не знает. */
export interface SbeLlmApi {
  getStatus(): { configured: boolean; apiUrl: string };
  complete(
    system: string,
    user: string,
    opts?: { model?: string; temperature?: number },
  ): Promise<string>;
  completeJson<T>(
    system: string,
    user: string,
    opts?: { model?: string; temperature?: number },
  ): Promise<T>;
  ask(
    question: string,
    opts?: {
      system?: string;
      context?: string;
      history?: Array<{ role: 'user' | 'assistant'; text: string }>;
      model?: string;
    },
  ): Promise<string>;
}

/** API сервиса авторизации YouGile (фаза 2). */
export interface SbeYougileApi {
  getStatus(): { authenticated: boolean; companyId?: string; login?: string };
  authenticate(): Promise<void>;
  client: {
    getProjects(): Promise<unknown[]>;
    getBoards(): Promise<unknown[]>;
    getColumns(boardId?: string): Promise<unknown[]>;
    getColumnById(id: string): Promise<unknown>;
    getUsers(): Promise<unknown[]>;
    getTasks(): Promise<unknown[]>;
    getTaskById(id: string): Promise<unknown>;
    createTask(payload: unknown): Promise<unknown>;
    updateTask(id: string, patch: unknown): Promise<unknown>;
    getGroupChats(): Promise<unknown[]>;
    getChatMessages(chatId: string): Promise<unknown[]>;
    sendChatMessage(chatId: string, text: string): Promise<unknown>;
    getTaskChatSubscribers(taskId: string): Promise<string[]>;
    uploadFile(file: { name: string; data: ArrayBuffer }): Promise<string>;
  };
}

/** Плагин с открываемым UI: магазин вызывает open() вместо собственного риббона/команды. */
export interface SbeOpenableApi {
  open(): Promise<void>;
}

/** API плагина «Мастер презентаций». */
export interface SbePresentationsApi extends SbeOpenableApi {}

/** Событие календаря от источника. */
export interface CalendarEvent {
  id: string;          // id в источнике (например, id задачи)
  title: string;       // название задачи
  start: number;       // дата события (дедлайн, ms)
  status: 'active' | 'completed';  // статус в источнике
  type: string;        // «Тип события» = название проекта
}

/** API плагина «Календарь» — приёмник событий от других плагинов. */
export interface SbeCalendarApi extends SbeOpenableApi {
  upsert(source: string, events: CalendarEvent[]): Promise<void>;
  remove(source: string, ids: string[]): Promise<void>;
  getSources(): string[];
}

/** API плагина «Задачи». open(taskId?) открывает конкретную задачу. */
export interface SbeTasksApi extends SbeOpenableApi {
  open(taskId?: string): Promise<void>;
}

/** API плагина «Письма». open() — точка входа из ЦУП. */
export interface SbeMailApi extends SbeOpenableApi {}

/** API плагина «Документы». open() — точка входа из ЦУП. */
export interface SbeDocumentsApi extends SbeOpenableApi {}

/** API плагина «Заявки на испытания». open() — точка входа из ЦУП. */
export interface SbeRequestsApi extends SbeOpenableApi {}

/** Карточка продукта по ЕКН (из sbe-ekn). */
export interface EknProductInfo {
  ekn: string;
  name: string;
  thickness: string;
  sto_number: string;
  sto_name: string;
  fetched_at: string;
  data: unknown;
}

/** Элемент поиска по ЕКН. */
export interface EknSearchResult {
  ekn: string;
  name: string;
  thickness: string;
  sto_number: string;
  sto_name: string;
}

/** API плагина «Справочник ЕКН». open() — точка входа из ЦУП;
 *  getProduct/search — сервисный доступ для других плагинов (sbe-requests и др.). */
export interface SbeEknApi extends SbeOpenableApi {
  getProduct(ekn: string, attributes?: string[]): Promise<EknProductInfo>;
  search(ekn: string): Promise<EknSearchResult[]>;
  /** Сохраняет карточку продукта, не найденного в QRC на момент оформления
   * заявки (sbe-requests) — не подтверждена в QRC, не перезатирает уже
   * QRC-подтверждённую запись (гарантирует сервер). */
  setManualProduct(
    ekn: string,
    name: string,
    thickness?: string,
    stoNumber?: string,
    stoName?: string,
  ): Promise<EknProductInfo>;
}

/** API плагина «ЛИМС». open() — точка входа из ЦУП. */
export interface SbeLimsApi extends SbeOpenableApi {}

/** API плагина «ЛИМС Мобайл» (сканирование QR → ввод результатов/калибровки на
 * планшете испытателя). open() — точка входа из мини-магазина sbe-mobile. */
export interface SbeLimsMobileApi extends SbeOpenableApi {}

/** API плагина «Контакты». open() — точка входа из ЦУП. */
export interface SbeContactsApi extends SbeOpenableApi {}

/** API плагина «LogicTEAM.007» (универсальный LLM-агент). open() — точка входа из ЦУП. */
export interface SbeAgentApi extends SbeOpenableApi {}

/** Словарь сервисов: ключ — id плагина, значение — его типизированное API. */
export interface SbeServiceMap {
  'sbe-apstore': SbeApstoreApi;
  'sbe-agent': SbeAgentApi;
  'sbe-calendar': SbeCalendarApi;
  'sbe-contacts': SbeContactsApi;
  'sbe-documents': SbeDocumentsApi;
  'sbe-ekn': SbeEknApi;
  'sbe-lims': SbeLimsApi;
  'sbe-lims-mobile': SbeLimsMobileApi;
  'sbe-llm': SbeLlmApi;
  'sbe-mailer': SbeMailApi;
  'sbe-presentations': SbePresentationsApi;
  'sbe-requests': SbeRequestsApi;
  'sbe-tasks': SbeTasksApi;
  'sbe-yougile': SbeYougileApi;
}

export type SbeServiceId = keyof SbeServiceMap;
