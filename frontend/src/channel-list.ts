import { append, h } from './dom';
import { Channels } from '../wailsjs/go/main/App';
import type { main } from '../wailsjs/go/models';

type Channel = main.Channel;

const ROW_HEIGHT = 34;
const OVERSCAN = 8;
const CHUNK = 200; // rows fetched per request

export interface ChannelListEvents {
  onPlay: (channel: Channel) => void;
  onToggleFavorite: (channel: Channel) => void;
  onError: (error: unknown) => void;
}

/**
 * Virtualized channel list. It holds every matching channel ID (cheap even
 * for 200k channels) and fetches row data for the visible window on demand,
 * so scrolling is O(visible rows) regardless of playlist size.
 */
export class ChannelList {
  #ids: number[] = [];
  #cache = new Map<number, Channel>();
  #loading = new Set<number>(); // chunk indexes in flight
  #generation = 0; // bumps on setIds; stale responses are dropped
  #selected = -1;
  #showGroup = true;
  #frame = 0;
  #drawn = ''; // "first:last" currently rendered; '' forces a redraw
  readonly #spacer = h('div', { class: 'channel-spacer' });

  constructor(
    readonly element: HTMLElement,
    readonly events: ChannelListEvents,
  ) {
    element.append(this.#spacer);
    element.addEventListener('scroll', () => this.#schedule(), { passive: true });
    element.addEventListener('keydown', (e) => this.#onKey(e));
    // Delegated pointer handling: rows are recreated on every render, so
    // listeners on the rows themselves would miss double-clicks.
    element.addEventListener('mousedown', (e) => {
      const index = this.#indexOf(e);
      if (index !== undefined) this.#select(index, false);
    });
    element.addEventListener('click', (e) => {
      const action = (e.target as HTMLElement).closest<HTMLElement>('[data-action]')?.dataset.action;
      const channel = this.#channelAt(this.#indexOf(e));
      if (!action || !channel) return;
      if (action === 'play') this.events.onPlay(channel);
      if (action === 'favorite') this.events.onToggleFavorite(channel);
    });
    element.addEventListener('dblclick', (e) => {
      if ((e.target as HTMLElement).closest('[data-action]')) return;
      const channel = this.#channelAt(this.#indexOf(e));
      if (channel) this.events.onPlay(channel);
    });
    new ResizeObserver(() => this.#schedule()).observe(element);
  }

  get size(): number {
    return this.#ids.length;
  }

  get selected(): Channel | undefined {
    return this.#cache.get(this.#ids[this.#selected]);
  }

  /** Replaces the list contents. */
  setIds(ids: number[], { showGroup = true, keepScroll = false } = {}): void {
    this.#ids = ids;
    this.#cache.clear();
    this.#loading.clear();
    this.#generation++;
    this.#showGroup = showGroup;
    if (!keepScroll) this.element.scrollTop = 0;
    const selected = keepScroll ? this.#selected : 0;
    this.#selected = ids.length ? Math.min(Math.max(selected, 0), ids.length - 1) : -1;
    this.#spacer.style.height = `${ids.length * ROW_HEIGHT}px`;
    this.#drawn = '';
    this.#render();
  }

  /** Updates one cached channel (e.g. after toggling favorite) and redraws. */
  update(channel: Channel): void {
    if (this.#cache.has(channel.id)) {
      this.#cache.set(channel.id, channel);
      this.#redraw();
    }
  }

  focus(): void {
    this.element.focus();
  }

  /** Schedules a render that rebuilds the rows even if the range is unchanged. */
  #redraw(): void {
    this.#drawn = '';
    this.#schedule();
  }

  #schedule(): void {
    if (!this.#frame) {
      this.#frame = requestAnimationFrame(() => {
        this.#frame = 0;
        this.#render();
      });
    }
  }

  #visibleRange(): [number, number] {
    const first = Math.max(0, Math.floor(this.element.scrollTop / ROW_HEIGHT) - OVERSCAN);
    const last = Math.min(
      this.#ids.length - 1,
      Math.ceil((this.element.scrollTop + this.element.clientHeight) / ROW_HEIGHT) + OVERSCAN,
    );
    return [first, last];
  }

  #render(): void {
    const [first, last] = this.#visibleRange();
    const range = `${first}:${last}`;
    if (range === this.#drawn) return; // scrolled by less than a row
    this.#drawn = range;
    const rows: HTMLElement[] = [];
    for (let i = first; i <= last; i++) {
      const channel = this.#cache.get(this.#ids[i]);
      if (!channel) void this.#load(Math.floor(i / CHUNK));
      rows.push(this.#row(i, channel));
    }
    this.#spacer.replaceChildren(...rows);
  }

  #row(index: number, channel: Channel | undefined): HTMLElement {
    const selected = index === this.#selected;
    const row = h('div', {
      class: `channel-row${selected ? ' selected' : ''}`,
      role: 'option',
      'aria-selected': String(selected),
      dataset: { index: String(index) },
    });
    row.style.transform = `translateY(${index * ROW_HEIGHT}px)`;

    if (!channel) {
      row.append(h('span', { class: 'channel-name placeholder' }, '…'));
      return row;
    }
    append(
      row,
      h(
        'button',
        {
          class: `star${channel.favorite ? ' on' : ''}`,
          type: 'button',
          title: channel.favorite ? 'Remove from favorites (F)' : 'Add to favorites (F)',
          'aria-pressed': String(channel.favorite),
          dataset: { action: 'favorite' },
        },
        channel.favorite ? '★' : '☆',
      ),
      h('span', { class: 'channel-name', title: channel.name }, channel.name),
      this.#showGroup && h('span', { class: 'channel-group' }, channel.group || 'Ungrouped'),
      h(
        'button',
        {
          class: 'play',
          type: 'button',
          title: 'Play (Enter)',
          dataset: { action: 'play' },
        },
        '▶ Play',
      ),
    );
    return row;
  }

  async #load(chunk: number): Promise<void> {
    if (this.#loading.has(chunk)) return;
    this.#loading.add(chunk);
    const generation = this.#generation;
    try {
      const channels = await Channels(this.#ids.slice(chunk * CHUNK, (chunk + 1) * CHUNK));
      if (generation !== this.#generation) return;
      for (const channel of channels) this.#cache.set(channel.id, channel);
      this.#redraw();
    } catch (error) {
      if (generation === this.#generation) this.events.onError(error);
    } finally {
      if (generation === this.#generation) this.#loading.delete(chunk);
    }
  }

  #indexOf(e: Event): number | undefined {
    const row = (e.target as HTMLElement).closest<HTMLElement>('.channel-row');
    return row?.dataset.index === undefined ? undefined : Number(row.dataset.index);
  }

  #channelAt(index: number | undefined): Channel | undefined {
    return index === undefined ? undefined : this.#cache.get(this.#ids[index]);
  }

  #select(index: number, scroll = true): void {
    if (!this.#ids.length) return;
    this.#selected = Math.min(Math.max(index, 0), this.#ids.length - 1);
    if (scroll) {
      const top = this.#selected * ROW_HEIGHT;
      const { scrollTop, clientHeight } = this.element;
      if (top < scrollTop) this.element.scrollTop = top;
      else if (top + ROW_HEIGHT > scrollTop + clientHeight) this.element.scrollTop = top + ROW_HEIGHT - clientHeight;
    }
    this.#redraw();
  }

  #onKey(e: KeyboardEvent): void {
    const page = Math.max(1, Math.floor(this.element.clientHeight / ROW_HEIGHT) - 1);
    const moves: Record<string, number> = {
      ArrowDown: this.#selected + 1,
      ArrowUp: this.#selected - 1,
      PageDown: this.#selected + page,
      PageUp: this.#selected - page,
      Home: 0,
      End: this.#ids.length - 1,
    };
    if (e.key in moves) {
      e.preventDefault();
      this.#select(moves[e.key]);
      return;
    }
    const channel = this.selected;
    if (!channel) return;
    if (e.key === 'Enter') {
      e.preventDefault();
      this.events.onPlay(channel);
    } else if (e.key.toLowerCase() === 'f' && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      this.events.onToggleFavorite(channel);
    }
  }
}
