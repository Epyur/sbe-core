import { App, Notice, requestUrl } from 'obsidian';
import type { RemoteManifest } from './types';
import { INSTALL_FILES, rawUrl } from './registry';
import { errorMessage } from './utils/errors';

export interface InstallResult {
  ok: boolean;
  error?: string;
}

function pluginBaseDir(app: App, dir: string): string {
  return `${app.vault.configDir}/plugins/${dir}`;
}

/** Читает manifest.json установленного плагина (null, если папки/файла нет). */
export async function readLocalManifest(app: App, dir: string): Promise<RemoteManifest | null> {
  try {
    const base = pluginBaseDir(app, dir);
    const exists = await app.vault.adapter.exists(`${base}/manifest.json`);
    if (!exists) return null;
    const content = await app.vault.adapter.read(`${base}/manifest.json`);
    return JSON.parse(content) as RemoteManifest;
  } catch (e: unknown) {
    console.warn(`SBE: не удалось прочитать manifest плагина «${dir}»:`, errorMessage(e));
    return null;
  }
}

/** Проверяет, загружен ли плагин с данным id в текущей сессии Obsidian. */
export function isPluginEnabled(app: App, id: string): boolean {
  const plugins = (app as unknown as { plugins?: { plugins?: Record<string, unknown> } }).plugins;
  return !!(plugins?.plugins && plugins.plugins[id]);
}

/** Очищает кэш require для main.js целевого плагина, чтобы enablePlugin загрузил новый код. */
function clearRequireCache(app: App, dir: string): void {
  const nodeRequire = (globalThis as unknown as { require?: NodeRequire }).require;
  if (!nodeRequire) return;
  const pluginDir = (app.vault.configDir + '/plugins/' + dir).replace(/\\/g, '/');
  try {
    delete nodeRequire.cache[nodeRequire.resolve(pluginDir + '/main.js')];
  } catch {
    // Файл мог быть ещё не загружен — не ошибка.
  }
}

/** Перезагружает плагин через disablePlugin/enablePlugin (недокументированный API Obsidian). */
async function reloadPlugin(app: App, id: string): Promise<void> {
  const plugins = (app as unknown as {
    plugins?: { disablePlugin(id: string): Promise<void>; enablePlugin(id: string): Promise<void> };
  }).plugins;
  if (!plugins) return;
  await plugins.disablePlugin(id);
  await plugins.enablePlugin(id);
}

/**
 * Скачивает manifest.json/main.js/styles.css из репозитория и перезапускает плагин.
 * Паттерн наследует текущий updater: requestUrl → adapter.write → require.cache → disable/enable.
 */
export async function installPlugin(
  app: App,
  opts: { dir: string; id: string; repo: string; branch: string },
): Promise<InstallResult> {
  const { dir, id, repo, branch } = opts;
  const adapter = app.vault.adapter;
  const base = pluginBaseDir(app, dir);

  try {
    if (!(await adapter.exists(base))) {
      await adapter.mkdir(base);
    }
    for (const file of INSTALL_FILES) {
      const url = rawUrl(repo, branch, file);
      const res = await requestUrl({ url, throw: false });
      if (res.status >= 400) throw new Error(`HTTP ${res.status} для ${file}`);
      await adapter.write(`${base}/${file}`, res.text);
    }
    clearRequireCache(app, dir);
    await reloadPlugin(app, id);
    return { ok: true };
  } catch (e: unknown) {
    const msg = errorMessage(e);
    new Notice(`ЦУП: не удалось установить «${id}»: ${msg}`);
    return { ok: false, error: msg };
  }
}
