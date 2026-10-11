import { h } from './dom';

/**
 * Shows a modal <dialog> built from `content` and resolves with the
 * returnValue of the button that closed it ("" when dismissed with Escape).
 * `onSubmit` may veto closing (return false) to keep the dialog open, e.g.
 * while validating.
 */
export const showDialog = (
  title: string,
  content: Node[],
  buttons: { label: string; value: string; kind?: 'primary' | 'danger' }[],
  onSubmit?: (value: string, form: HTMLFormElement) => boolean | Promise<boolean>,
): Promise<string> => {
  const form = h(
    'form',
    { method: 'dialog' },
    h('h2', null, title),
    ...content,
    h(
      'div',
      { class: 'dialog-buttons' },
      ...buttons.map(({ label, value, kind }) =>
        h('button', { type: value ? 'submit' : 'button', value, class: kind ?? '', dataset: { value } }, label),
      ),
    ),
  );
  const dialog = h('dialog', null, form);
  document.body.append(dialog);

  return new Promise((resolve) => {
    let busy = false;
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      if (busy) return;
      const value = (e.submitter as HTMLButtonElement | null)?.value ?? buttons.find((b) => b.value)?.value ?? '';
      busy = true;
      try {
        if (!onSubmit || (await onSubmit(value, form))) dialog.close(value);
      } finally {
        busy = false;
      }
    });
    form.querySelector('button[data-value=""]')?.addEventListener('click', () => dialog.close(''));
    dialog.addEventListener('close', () => {
      dialog.remove();
      resolve(dialog.returnValue);
    });
    dialog.showModal();
  });
};

export const confirmDialog = async (
  title: string,
  message: string,
  okLabel: string,
  kind: 'primary' | 'danger' = 'primary',
): Promise<boolean> =>
  (await showDialog(title, [h('p', null, message)], [
    { label: 'Cancel', value: '' },
    { label: okLabel, value: 'ok', kind },
  ])) === 'ok';

export const promptDialog = async (title: string, label: string, value: string): Promise<string | null> => {
  const input = h('input', { type: 'text', value, required: true, autocomplete: 'off', spellcheck: false });
  const result = await showDialog(title, [h('label', null, label, input)], [
    { label: 'Cancel', value: '' },
    { label: 'Save', value: 'ok', kind: 'primary' },
  ]);
  return result === 'ok' ? input.value.trim() : null;
};
