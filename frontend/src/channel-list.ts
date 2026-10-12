import { append, groupLabel, h, setSelected } from './dom';
import { Channels } from '../wailsjs/go/main/App';
import type { main } from '../wailsjs/go/models';

type Channel = main.Channel;

const ROW_HEIGHT = 34;
const OVERSCAN = 8;
const CHUNK = 200; // rows fetched per request

export interface ChannelListEvents {
  onPlay: (channel: Channel) => void;
  onToggleFavorite: (channel: Channel) => void;
  onCopyURL: (channel: Channel) => void;
  /** Right-click or the Menu key on a row; (x, y) is where to show a menu. */
  onContextMenu: (channel: Channel, x: number, y: number) => void;
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
  #drawn = ''; // "first:last" currently rendered; '' forces a render
  readonly #rows = new Map<number, HTMLElement>(); // drawn rows by index
  readonly #spacer = h('div', { class: 'channel-spacer' });

  constructor(
    readonly element: HTMLElement,
    readonly events: ChannelListEvents,
  ) {
    element.append(this.#spacer);
    element.addEventListener('scroll', () => this.#schedule(), { passive: true });
    element.addEventListener('keydown', (e) => this.#onKey(e));
    // Delegated pointer handling: rows are recreated as they scroll into
    // view or their data changes, so listeners on them would be lost.
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
    element.addEventListener('contextmenu', (e) => {
      const channel = this.#channelAt(this.#indexOf(e));
      if (!channel) return;
      e.preventDefault();
      this.element.focus();
      this.events.onContextMenu(channel, e.clientX, e.clientY);
    });
    new ResizeObserver(() => this.#schedule()).observe(element);
  }

  get size(): number {
    return this.#ids.length;
  }

  /** Replaces the list contents. */
  setIds(ids: number[], { showGroup = true, keepScroll = false } = {}): void {
    this.#ids = ids;
    this.#cache.clear();
    this.#loading.clear();
    this.#generation++;
    this.#showGroup = showGroup;
    if (!keepScroll) this.element.scrollTop = 0;
    this.#selected = ids.length ? this.#clamp(keepScroll ? this.#selected : 0) : -1;
    this.#spacer.style.height = `${ids.length * ROW_HEIGHT}px`;
    this.#rows.clear();
    this.#spacer.replaceChildren();
    this.#drawn = '';
    this.#render();
  }

  /** Updates one cached channel (e.g. after toggling favorite) and redraws its row. */
  update(channel: Channel): void {
    if (this.#cache.has(channel.id)) {
      this.#cache.set(channel.id, channel);
      this.#rebuild((i) => this.#ids[i] === channel.id);
    }
  }

  focus(): void {
    this.element.focus();
  }

  #current(): Channel | undefined {
    return this.#cache.get(this.#ids[this.#selected]);
  }

  #clamp(index: number): number {
    return Math.min(Math.max(index, 0), this.#ids.length - 1);
  }

  /** Rebuilds the drawn rows whose index matches. */
  #rebuild(matches: (index: number) => boolean): void {
    for (const [i, row] of this.#rows) {
      if (!matches(i)) continue;
      const fresh = this.#row(i, this.#cache.get(this.#ids[i]));
      row.replaceWith(fresh);
      this.#rows.set(i, fresh);
    }
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
    // Keep the rows still in range; build only those scrolled into view,
    // keeping the DOM in index order for assistive technology.
    for (const [i, row] of this.#rows) {
      if (i < first || i > last) {
        row.remove();
        this.#rows.delete(i);
      }
    }
    const top = this.#rows.size ? Math.min(...this.#rows.keys()) : Infinity;
    const above: HTMLElement[] = [];
    const below: HTMLElement[] = [];
    const missing = new Set<number>(); // chunks to load
    for (let i = first; i <= last; i++) {
      if (this.#rows.has(i)) continue;
      const channel = this.#cache.get(this.#ids[i]);
      if (!channel) missing.add(Math.floor(i / CHUNK));
      const row = this.#row(i, channel);
      this.#rows.set(i, row);
      (i < top ? above : below).push(row);
    }
    this.#spacer.prepend(...above);
    this.#spacer.append(...below);
    for (const chunk of missing) void this.#load(chunk);
  }

  #row(index: number, channel: Channel | undefined): HTMLElement {
    const row = h('div', { class: 'channel-row', role: 'option', dataset: { index: String(index) } });
    setSelected(row, index === this.#selected);
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
      this.#showGroup && h('span', { class: 'channel-group' }, groupLabel(channel.group)),
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
      this.#rebuild((i) => Math.floor(i / CHUNK) === chunk);
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
    this.#selected = this.#clamp(index);
    if (scroll) {
      const top = this.#selected * ROW_HEIGHT;
      const { scrollTop, clientHeight } = this.element;
      if (top < scrollTop) this.element.scrollTop = top;
      else if (top + ROW_HEIGHT > scrollTop + clientHeight) this.element.scrollTop = top + ROW_HEIGHT - clientHeight;
    }
    // Restyle the existing rows rather than rebuilding them: selection
    // happens on mousedown, and replacing the pressed button before mouseup
    // makes the browser drop the click (and dblclick).
    for (const row of this.#spacer.children as HTMLCollectionOf<HTMLElement>) {
      setSelected(row, Number(row.dataset.index) === this.#selected);
    }
    this.#schedule(); // only rebuilds if scrolling changed the visible range
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
    const channel = this.#current();
    if (!channel) return;
    if (e.key === 'Enter') {
      e.preventDefault();
      this.events.onPlay(channel);
    } else if (e.key.toLowerCase() === 'f' && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      this.events.onToggleFavorite(channel);
    } else if (e.key.toLowerCase() === 'c' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      this.events.onCopyURL(channel);
    } else if (e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey)) {
      e.preventDefault();
      const row = this.#spacer.querySelector('.channel-row.selected')?.getBoundingClientRect();
      if (row) this.events.onContextMenu(channel, row.left + 24, row.bottom);
    }
  }
}
