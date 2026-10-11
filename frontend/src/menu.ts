import { h } from './dom';

export interface MenuItem {
  label: string;
  shortcut?: string;
  action: () => void;
}

let closeOpen: (() => void) | undefined;

/**
 * Shows a context menu at (x, y), kept inside the window. It closes on
 * Escape, Tab, a click elsewhere, scrolling, resizing or choosing an item;
 * focus then returns to where it was.
 */
export const showMenu = (x: number, y: number, items: MenuItem[]): void => {
  closeOpen?.();
  const previous = document.activeElement as HTMLElement | null;
  const buttons = items.map(({ label, shortcut, action }) =>
    h(
      'button',
      {
        type: 'button',
        role: 'menuitem',
        tabindex: -1,
        onclick: () => {
          close();
          action();
        },
      },
      h('span', null, label),
      shortcut && h('kbd', null, shortcut),
    ),
  );
  const menu = h('div', { class: 'menu', role: 'menu' }, ...buttons);

  const focusAt = (i: number) => buttons[(i + buttons.length) % buttons.length].focus();
  menu.addEventListener('keydown', (e) => {
    const i = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const moves: Record<string, number> = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: -1 };
    if (e.key in moves) {
      e.preventDefault();
      focusAt(moves[e.key]);
    } else if (e.key === 'Escape' || e.key === 'Tab') {
      e.preventDefault();
      close();
    }
  });
  const onPointer = (e: Event) => {
    if (!menu.contains(e.target as Node)) close();
  };
  const onDismiss = () => close();

  function close(): void {
    if (closeOpen !== close) return;
    closeOpen = undefined;
    menu.remove();
    document.removeEventListener('mousedown', onPointer, true);
    document.removeEventListener('scroll', onDismiss, true);
    window.removeEventListener('resize', onDismiss);
    window.removeEventListener('blur', onDismiss);
    previous?.focus();
  }

  document.body.append(menu);
  const { width, height } = menu.getBoundingClientRect();
  menu.style.left = `${Math.max(0, Math.min(x, innerWidth - width))}px`;
  menu.style.top = `${Math.max(0, Math.min(y, innerHeight - height))}px`;
  document.addEventListener('mousedown', onPointer, true);
  document.addEventListener('scroll', onDismiss, true);
  window.addEventListener('resize', onDismiss);
  window.addEventListener('blur', onDismiss);
  closeOpen = close;
  focusAt(0);
};
