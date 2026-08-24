import { requestUrl } from 'obsidian';
import type { RegistryFile, RegistryPluginEntry, RemoteManifest } from './types';

export const DEFAULT_REGISTRY_URL =
  'https://epyur.fvds.ru/registry.json';

export const PLUGIN_FILES = ['manifest.json', 'main.js', 'styles.css'];

/** Собирает raw URL файла в репозитории. */
export function rawUrl(repo: string, branch: string, file: string): string {
  return `https://raw.githubusercontent.com/${repo}/${branch}/${file}`;
}

/** Семантическое сравнение версий x.y.z: true, если remote новее local. */
export function isNewer(remote: string, local: string): boolean {
  const r = remote.split('.').map(Number);
  const l = local.split('.').map(Number);
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
  const branch = entry.branch || 'main';
  return fetchJson<RemoteManifest>(rawUrl(entry.repo, branch, 'manifest.json'));
}

export { PLUGIN_FILES as INSTALL_FILES };
