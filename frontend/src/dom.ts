// Minimal DOM builder. All text goes through text nodes / textContent, never
// HTML parsing: channel and group names come from untrusted playlists, and
// with Go methods callable from JS, an XSS here would be serious (C3 in the
// design review). CI rejects HTML-parsing DOM sinks in frontend/src.

export type Child = Node | string | number | false | null | undefined;
type Listener = (event: Event) => void;
type Props = Record<string, unknown>;

export const h = <K extends keyof HTMLElementTagNameMap>(
  tag: K,
  props: Props | null = null,
  ...children: Child[]
): HTMLElementTagNameMap[K] => {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props ?? {})) {
    if (value === undefined || value === null || value === false) continue;
    if (/html/i.test(key)) throw new Error(`refusing to set ${key}`);
    if (key === 'class') {
      el.className = String(value);
    } else if (key === 'dataset') {
      Object.assign(el.dataset, value);
    } else if (key.startsWith('on') && typeof value === 'function') {
      el.addEventListener(key.slice(2).toLowerCase(), value as Listener);
    } else if (key in el) {
      (el as unknown as Props)[key] = value;
    } else {
      el.setAttribute(key, value === true ? '' : String(value));
    }
  }
  append(el, ...children);
  return el;
};

export const append = (parent: Node, ...children: Child[]): void => {
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    // A typeof test (not instanceof) lets CodeQL see strings never reach appendChild as-is.
    parent.appendChild(typeof child === 'object' ? child : document.createTextNode(String(child)));
  }
};

export const $ = <T extends HTMLElement = HTMLElement>(selector: string): T => {
  const el = document.querySelector<T>(selector);
  if (!el) throw new Error(`missing element ${selector}`);
  return el;
};

const number = new Intl.NumberFormat();

export const formatCount = (n: number): string => number.format(n);

/** "1 channel", "1,234 channels". */
export const plural = (n: number, word: string): string => `${formatCount(n)} ${word}${n === 1 ? '' : 's'}`;

/** A channel group's display name. */
export const groupLabel = (name: string): string => name || 'Ungrouped';

const relative = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

export const timeAgo = (unixSeconds: number): string => {
  if (!unixSeconds) return 'never';
  const seconds = unixSeconds - Date.now() / 1000;
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ['day', 86400],
    ['hour', 3600],
    ['minute', 60],
  ];
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return relative.format(Math.round(seconds / size), unit);
  }
  return 'just now';
};

export const debounce = <A extends unknown[]>(fn: (...args: A) => void, ms: number) => {
  let timer: number | undefined;
  return (...args: A) => {
    clearTimeout(timer);
    timer = window.setTimeout(() => fn(...args), ms);
  };
};

/** Turns a rejected Wails call (usually a plain string) into a message. */
export const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : typeof error === 'string' ? error : 'Something went wrong';
