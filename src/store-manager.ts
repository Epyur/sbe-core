import { App } from 'obsidian';
import { fetchRegistry, fetchRemoteManifest, isNewer } from './registry';
import { installPlugin, readLocalManifest } from './installer';
import { errorMessage } from './utils/errors';
import type {
  InstalledPlugin,
  PluginCard,
  PluginState,
  RegistryFile,
  RegistryPluginEntry,
  RemoteManifest,
  UpdateSummary,
} from './types';

function computeState(entry: RegistryPluginEntry, local: RemoteManifest | null, remote: RemoteManifest | null): PluginState {
  if (!local) return 'not-installed';
  if (remote && isNewer(remote.version, local.version)) return 'update-available';
  return entry.required ? 'required' : 'installed';
}

/**
 * Управляет состоянием магазина: реестр, локальные манифесты, установка и обновления.
 * Сам по себе ничего не сохраняет на диск — настройки (registryUrl) хранит плагин.
 */
export class StoreManager {
  private app: App;
  private registryUrl = '';
  private registry: RegistryFile | null = null;
  private cards: PluginCard[] = [];
  private localManifests = new Map<string, RemoteManifest | null>();

  constructor(app: App) {
    this.app = app;
  }

  setRegistryUrl(url: string): void {
    this.registryUrl = url;
  }

  getRegistry(): RegistryPluginEntry[] {
    return this.registry?.plugins ?? [];
  }

  getCards(): PluginCard[] {
    return this.cards;
  }

  private findEntry(id: string): RegistryPluginEntry | undefined {
    return this.registry?.plugins.find(p => p.id === id || p.dir === id);
  }

  /** Скачивает реестр и пересчитывает карточки (локальные и удалённые манифесты). */
  async refresh(): Promise<void> {
    if (!this.registryUrl) {
      this.registry = null;
      this.cards = [];
      return;
    }
    try {
      this.registry = await fetchRegistry(this.registryUrl);
    } catch (e: unknown) {
      console.warn(`ЦУП: не удалось загрузить реестр ${this.registryUrl}:`, errorMessage(e));
      this.registry = null;
      this.cards = [];
      return;
    }

    const cards: PluginCard[] = [];
    for (const entry of this.registry.plugins) {
      let local = this.localManifests.has(entry.dir)
        ? this.localManifests.get(entry.dir) ?? null
        : await readLocalManifest(this.app, entry.dir);
      this.localManifests.set(entry.dir, local);

      let remote: RemoteManifest | null = null;
      try {
        remote = await fetchRemoteManifest(entry);
      } catch (e: unknown) {
        console.warn(`ЦУП: не удалось получить манифест «${entry.id}»:`, errorMessage(e));
      }
      cards.push({ entry, local, remote, state: computeState(entry, local, remote) });
    }
    this.cards = cards;
  }

  getPluginState(id: string): PluginState {
    const card = this.cards.find(c => c.entry.id === id || c.entry.dir === id);
    return card?.state ?? 'not-installed';
  }

  listInstalled(): InstalledPlugin[] {
    return this.cards
      .filter(c => c.local)
      .map(c => ({
        id: c.entry.id,
        dir: c.entry.dir,
        name: c.local?.name || c.entry.name,
        version: c.local?.version || '',
        description: c.local?.description,
        hasView: !!c.entry.hasView,
      }));
  }

  /** Полная проверка: реестр + манифесты + список доступных обновлений. */
  async checkUpdates(): Promise<UpdateSummary> {
    await this.refresh();
    const updates = this.cards
      .filter(c => c.state === 'update-available' && c.local && c.remote)
      .map(c => ({
        id: c.entry.id,
        name: c.entry.name,
        from: (c.local as RemoteManifest).version,
        to: (c.remote as RemoteManifest).version,
      }));
    return { checkedAt: Date.now(), updates };
  }

  private async apply(id: string, required: boolean): Promise<void> {
    const entry = this.findEntry(id);
    if (!entry) throw new Error(`Плагин «${id}» не найден в реестре`);
    if (required && !entry.required) {
      throw new Error(`Плагин «${entry.name}» нельзя установить автоматически`);
    }
    const res = await installPlugin(this.app, {
      dir: entry.dir,
      id: entry.id,
      repo: entry.repo,
      branch: entry.branch || 'main',
      hashes: entry.hashes,
      selfHosted: entry.selfHosted,
    });
    if (!res.ok) throw new Error(res.error || 'Не удалось установить');
    this.localManifests.set(entry.dir, await readLocalManifest(this.app, entry.dir));
    await this.refresh();
  }

  async install(id: string): Promise<void> {
    await this.apply(id, false);
  }

  async update(id: string): Promise<void> {
    await this.apply(id, false);
  }

  async updateAll(): Promise<{ updated: string[]; failed: string[] }> {
    const updates = this.cards.filter(c => c.state === 'update-available');
    const updated: string[] = [];
    const failed: string[] = [];
    for (const card of updates) {
      try {
        await this.apply(card.entry.id, false);
        updated.push(card.entry.id);
      } catch (e: unknown) {
        console.error(`ЦУП: обновление «${card.entry.id}» не удалось:`, errorMessage(e));
        failed.push(card.entry.id);
      }
    }
    await this.refresh();
    return { updated, failed };
  }
}
