import { requestUrl } from 'obsidian';
import type { RegistryFile, RegistryPluginEntry, RemoteManifest } from './types';

export const DEFAULT_REGISTRY_URL =
  'https://epyur.fvds.ru/registry.json';

export const PLUGIN_FILES = ['manifest.json', 'main.js', 'styles.css'];

/** Собирает raw URL файла в репозитории. */
export function rawUrl(repo: string, branch: string, file: string): string {
  return `https://raw.githubusercontent.com/${repo}/${branch}/${file}`;
}

/** Откуда качать файл конкретной записи реестра — раздача с epyur.fvds.ru/plugins/*,
 * если entry.selfHosted (2026-08-29, см. docs/superpowers/specs/
 * 2026-08-29-sbe-plugin-file-upload-design.md), иначе как раньше — raw.githubusercontent.com
 * (repo/branch). Единственное место, что решает этот выбор — installer.ts и
 * fetchRemoteManifest ниже оба идут через эту функцию, не дублируют условие. */
export function pluginFileUrl(entry: Pick<RegistryPluginEntry, 'dir' | 'repo' | 'branch' | 'selfHosted'>, file: string): string {
  if (entry.selfHosted) return `https://epyur.fvds.ru/plugins/${entry.dir}/${file}`;
  return rawUrl(entry.repo, entry.branch || 'main', file);
}

/** Разбирает сегмент версии в число: "5" → 5, "5b" → 5 (буквенный суффикс —
 * локальная пометка «ветка backend не синхронизирована с main», см. корневой
 * AGENTS.md — не часть числового значения). `Number("5b")` дал бы `NaN`, а
 * `NaN || 0` в сравнении ниже тихо обнулял бы сегмент — версия с суффиксом
 * (реально более новая) выглядела бы СТАРШЕ и ЦУП ложно предлагал бы откат
 * на версию с main. */
function parseVersionSegment(seg: string): number {
  return parseInt(seg, 10) || 0;
}

/** Семантическое сравнение версий x.y.z: true, если remote новее local. */
export function isNewer(remote: string, local: string): boolean {
  const r = remote.split('.').map(parseVersionSegment);
  const l = local.split('.').map(parseVersionSegment);
  for (let i = 0; i < Math.max(r.length, l.length); i++) {
    const rv = r[i] || 0;
    const lv = l[i] || 0;
    if (rv > lv) return true;
    if (rv < lv) return false;
  }
  return false;
}

/** Выполняет GET и возвращает JSON (кэш-бастер против HTTP-кэша Obsidian). */
export async function fetchJson<T>(url: string): Promise<T> {
  const sep = url.includes('?') ? '&' : '?';
  const res = await requestUrl({ url: `${url}${sep}_t=${Date.now()}`, throw: false });
  if (res.status >= 400) throw new Error(`HTTP ${res.status} для ${url}`);
  return res.json as T;
}

/** Скачивает registry.json. */
export async function fetchRegistry(url: string): Promise<RegistryFile> {
  return fetchJson<RegistryFile>(url);
}

/** Скачивает manifest.json плагина из его репозитория. */
export async function fetchRemoteManifest(entry: RegistryPluginEntry): Promise<RemoteManifest> {
  return fetchJson<RemoteManifest>(pluginFileUrl(entry, 'manifest.json'));
}

export { PLUGIN_FILES as INSTALL_FILES };
