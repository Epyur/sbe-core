import type { SbeOpenableApi, SbeServiceId, SbeServiceMap } from './types';

export const SBE_NS = 'SBE';

export interface SbeServiceMeta {
  version: string;
  name: string;
}

export interface SbeServiceRecord {
  api: unknown;
  meta: SbeServiceMeta;
}

export interface SbeNamespace {
  services: Record<string, SbeServiceRecord>;
}

declare global {
  interface Window {
    SBE?: SbeNamespace;
  }
}

/** Возвращает (и при необходимости создаёт) глобальное пространство SBE. */
export function getSbeNamespace(): SbeNamespace {
  if (!window.SBE) {
    window.SBE = { services: {} };
  }
  return window.SBE;
}

/** Сервисный плагин публикует своё API при onload. */
export function publishService<T>(id: string, api: T, meta: SbeServiceMeta): void {
  getSbeNamespace().services[id] = { api, meta };
}

/** Сервисный плагин снимает API при onunload. */
export function unpublishService(id: string): void {
  delete getSbeNamespace().services[id];
}

/** Синхронный доступ к сервису (null, если ещё не опубликован). */
export function getServiceSync<K extends SbeServiceId>(id: K): SbeServiceMap[K] | null {
  const rec = getSbeNamespace().services[id];
  return rec ? (rec.api as SbeServiceMap[K]) : null;
}

/**
 * Асинхронное получение сервиса: ждёт появления (поллинг 200 мс) до таймаута.
 * Порядок загрузки плагинов не важен. При таймауте — понятная ошибка.
 */
export async function getService<K extends SbeServiceId>(
  id: K,
  timeoutMs = 15000,
): Promise<SbeServiceMap[K]> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const service = getServiceSync(id);
    if (service) return service;
    await new Promise(resolve => window.setTimeout(resolve, 200));
  }
  throw new Error(`Сервис «${id}» недоступен. Установите и включите плагин «${getServiceName(id)}» из ЦУП СБЕ ПМиПИР.`);
}

function getServiceName(id: string): string {
  switch (id) {
    case 'sbe-llm':
      return 'SBE LLM Center';
    case 'sbe-apstore':
      return 'ЦУП СБЕ ПМиПИР';
    case 'sbe-presentations':
      return 'Мастер презентаций';
    case 'sbe-mailer':
      return 'Письма';
    case 'sbe-documents':
      return 'Документы';
    case 'sbe-requests':
      return 'Заявки на испытания';
    case 'sbe-ekn':
      return 'Справочник ЕКН';
    case 'sbe-lims':
      return 'ЛИМС';
    case 'sbe-lims-mobile':
      return 'ЛИМС Мобайл';
    case 'sbe-contacts':
      return 'Контакты';
    case 'sbe-dashboards':
      return 'Logicteam.Дашборды';
    case 'sbe-agent':
      return 'LogicTEAM.007';
    case 'sbe-yougile':
      return 'SBE YouGile';
    default:
      return id;
  }
}

/** Проверяет, что API сервиса можно открыть как вьюху (метод open()). */
export function isOpenable(api: unknown): api is SbeOpenableApi {
  return typeof api === 'object' && api !== null && 'open' in api
    && typeof (api as { open?: unknown }).open === 'function';
}
