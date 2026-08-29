import { App, Notice, requestUrl } from 'obsidian';
import type { RegistryPluginEntry, RemoteManifest } from './types';
import { INSTALL_FILES, pluginFileUrl } from './registry';
import { errorMessage } from './utils/errors';

export interface InstallResult {
  ok: boolean;
  error?: string;
  /** Предупреждение (например, установка без контроля целостности). */
  warning?: string;
}

/** SHA-256 от байтов в hex (crypto.subtle доступен в рендерере Obsidian). */
async function sha256Hex(bytes: ArrayBuffer): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/** Безопасное имя каталога плагина: только буквы/цифры/._-, без путей и traversal
 *  (ревью B4: `dir` приходит из внешнего реестра). */
export function safePluginDir(dir: string): string {
  const d = (dir || '').trim();
  if (!/^[a-z0-9][a-z0-9._-]*$/i.test(d) || d.includes('..')) {
    throw new Error(`Недопустимое имя каталога плагина: «${dir}»`);
  }
  return d;
}

function pluginBaseDir(app: App, dir: string): string {
  return `${app.vault.configDir}/plugins/${dir}`;
}

/** Читает manifest.json установленного плагина (null, если папки/файла нет или путь небезопасен). */
export async function readLocalManifest(app: App, dir: string): Promise<RemoteManifest | null> {
  try {
    const base = pluginBaseDir(app, safePluginDir(dir));
    const exists = await app.vault.adapter.exists(`${base}/manifest.json`);
    if (!exists) return null;
    const content = await app.vault.adapter.read(`${base}/manifest.json`);
    return JSON.parse(content) as RemoteManifest;
  } catch (e: unknown) {
    if ((e as Error)?.message?.includes('Недопустимое имя')) return null;
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
 * Безопасность (ревью B4):
 *  - `dir` валидируется (запрет traversal);
 *  - если в записи реестра заданы SHA-256 хеши файлов — скачивание проверяется
 *    до записи на диск; при несовпадении установка прерывается;
 *  - если хеши не заданы — установка выполняется с предупреждением.
 */
export async function installPlugin(
  app: App,
  opts: {
    dir: string;
    id: string;
    repo: string;
    branch: string;
    hashes?: RegistryPluginEntry['hashes'];
    selfHosted?: boolean;
    /** Пропустить clearRequireCache + disable/enable — для самообновления плагина-установщика
     *  (перезагрузка самого себя во время выполнения небезопасна; после записи файлов
     *  вызывающий показывает пользователю кнопку перезапуска Obsidian). */
    skipReload?: boolean;
  },
): Promise<InstallResult> {
  try {
    const dir = safePluginDir(opts.dir);
    const { id, repo, branch } = opts;
    const hashes = opts.hashes ?? {};
    const adapter = app.vault.adapter;
    const base = pluginBaseDir(app, dir);

    if (!(await adapter.exists(base))) {
      await adapter.mkdir(base);
    }

    let integrityChecked = false;
    for (const file of INSTALL_FILES) {
      const url = pluginFileUrl({ dir, repo, branch, selfHosted: opts.selfHosted }, file);
      const res = await requestUrl({ url, method: 'GET', throw: false });
      if (res.status >= 400) throw new Error(`HTTP ${res.status} для ${file}`);

      const bytes = res.arrayBuffer;

      // Контроль целостности по SHA-256 из реестра (если хеш задан).
      const hashKey = file === 'manifest.json' ? 'manifest' : file === 'main.js' ? 'main' : file === 'styles.css' ? 'styles' : '';
      const expected = hashKey ? (hashes as Record<string, string | undefined>)[hashKey] : undefined;
      if (expected) {
        const actual = await sha256Hex(bytes);
        if (actual.toLowerCase() !== expected.toLowerCase()) {
          throw new Error(
            `Контрольная сумма ${file} не совпадает (ожидалось ${expected.slice(0, 12)}…, получено ${actual.slice(0, 12)}…). Установка прервана.`,
          );
        }
        integrityChecked = true;
      }

      await adapter.writeBinary(`${base}/${file}`, bytes);
    }

    if (!integrityChecked && Object.keys(hashes).length > 0) {
      // Хеши были, но ни один файл не проверен — предупреждаем.
      console.warn('SBE: ни один файл не прошёл проверку целостности.');
    }

    if (!opts.skipReload) {
      clearRequireCache(app, dir);
      await reloadPlugin(app, id);
    }
    return { ok: true };
  } catch (e: unknown) {
    const msg = errorMessage(e);
    new Notice(`ЦУП: не удалось установить «${opts.id}»: ${msg}`);
    return { ok: false, error: msg };
  }
}
