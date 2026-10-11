import './style.css';

import {
  AddPlaylistFile,
  AddPlaylistURL,
  CancelImport,
  ChannelIDs,
  ChooseVLC,
  DeletePlaylist,
  GetSettings,
  ListGroups,
  ListPlaylists,
  Play,
  RefreshPlaylist,
  RenamePlaylist,
  ResetVLC,
  SetFavorite,
  SetUserAgent,
} from '../wailsjs/go/main/App';
import type { main } from '../wailsjs/go/models';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { ChannelList } from './channel-list';
import { confirmDialog, promptDialog, showDialog } from './dialogs';
import { $, debounce, errorMessage, formatCount, h, timeAgo } from './dom';

type Selection = { kind: 'all' } | { kind: 'favorites' } | { kind: 'group'; name: string };

const state = {
  playlists: [] as main.Playlist[],
  playlistId: null as number | null,
  groups: [] as main.Group[],
  selection: { kind: 'all' } as Selection,
  search: '',
  importing: false,
};

const LAST_PLAYLIST_KEY = 'lastPlaylistId';

const ui = {
  playlists: $<HTMLUListElement>('#playlists'),
  groups: $<HTMLUListElement>('#groups'),
  search: $<HTMLInputElement>('#search'),
  title: $('#channels-title'),
  count: $('#channels-count'),
  empty: $('#empty'),
  status: $('#status'),
  cancel: $<HTMLButtonElement>('#cancel-import'),
  toasts: $('#toasts'),
  add: $<HTMLButtonElement>('#add-playlist'),
};

// ---------- feedback ----------

const toast = (message: string, kind: 'error' | 'info' = 'error'): void => {
  const el = h('div', { class: `toast ${kind}`, role: kind === 'error' ? 'alert' : 'status' }, message);
  el.addEventListener('click', () => el.remove());
  ui.toasts.append(el);
  setTimeout(() => el.remove(), kind === 'error' ? 9000 : 4000);
};

const fail = (error: unknown): void => toast(errorMessage(error));

const setStatus = (text: string): void => {
  ui.status.textContent = text;
};

// ---------- channel list ----------

const list = new ChannelList($('#channels'), {
  onPlay: async (channel) => {
    try {
      await Play(channel.id);
      setStatus(`Opened “${channel.name}” in VLC`);
    } catch (error) {
      fail(error);
    }
  },
  onToggleFavorite: async (channel) => {
    try {
      await SetFavorite(channel.id, !channel.favorite);
      if (state.selection.kind === 'favorites') {
        await loadChannels({ keepScroll: true });
      } else {
        list.update({ ...channel, favorite: !channel.favorite });
      }
    } catch (error) {
      fail(error);
    }
  },
  onError: fail,
});

const selectionLabel = (s: Selection): string =>
  s.kind === 'all' ? 'All channels' : s.kind === 'favorites' ? 'Favorites' : s.name || 'Ungrouped';

const loadChannels = async ({ keepScroll = false } = {}): Promise<void> => {
  const playlistId = state.playlistId;
  if (playlistId === null) {
    list.setIds([]);
    renderEmpty();
    return;
  }
  const { selection, search } = state;
  const ids = await ChannelIDs({
    playlistId,
    allGroups: selection.kind !== 'group',
    group: selection.kind === 'group' ? selection.name : '',
    favoritesOnly: selection.kind === 'favorites',
    search,
  });
  // Ignore responses that arrive after the user moved on.
  if (playlistId !== state.playlistId || selection !== state.selection || search !== state.search) return;
  list.setIds(ids, { showGroup: selection.kind !== 'group', keepScroll });
  ui.title.textContent = search ? `${selectionLabel(selection)} · “${search}”` : selectionLabel(selection);
  ui.count.textContent = `${formatCount(ids.length)} channel${ids.length === 1 ? '' : 's'}`;
  renderEmpty();
};

const renderEmpty = (): void => {
  const reason =
    state.playlists.length === 0
      ? h(
          'div',
          null,
          h('p', null, 'No playlists yet.'),
          h('button', { class: 'primary', type: 'button', onclick: () => void addPlaylist() }, 'Add your first playlist'),
        )
      : state.playlistId === null
        ? 'Select a playlist.'
        : list.size > 0
          ? null
          : state.search
            ? `No channels match “${state.search}”.`
            : state.selection.kind === 'favorites'
              ? 'No favorites yet. Press F or click ☆ on a channel to add one.'
              : 'This group is empty.';
  ui.empty.hidden = reason === null;
  ui.empty.replaceChildren(...(reason === null ? [] : [reason]));
};

// ---------- groups ----------

const renderGroups = (): void => {
  if (state.playlistId === null) {
    ui.groups.replaceChildren();
    return;
  }
  const total = state.groups.reduce((sum, g) => sum + g.count, 0);
  const item = (selection: Selection, label: string, count?: number, extraClass = '') => {
    const selected =
      selection.kind === state.selection.kind &&
      (selection.kind !== 'group' || (state.selection.kind === 'group' && selection.name === state.selection.name));
    return h(
      'li',
      {
        class: `list-item${selected ? ' selected' : ''} ${extraClass}`,
        role: 'option',
        'aria-selected': String(selected),
        tabindex: selected ? 0 : -1,
        onclick: () => void selectGroup(selection),
        onkeydown: (e: KeyboardEvent) => e.key === 'Enter' && void selectGroup(selection),
      },
      h('span', { class: 'item-name', title: label }, label),
      count !== undefined && h('span', { class: 'item-count' }, formatCount(count)),
    );
  };
  ui.groups.replaceChildren(
    item({ kind: 'all' }, 'All channels', total, 'special'),
    item({ kind: 'favorites' }, '★ Favorites', undefined, 'special'),
    h('li', { class: 'separator', role: 'presentation' }),
    ...state.groups.map((g) => item({ kind: 'group', name: g.name }, g.name || 'Ungrouped', g.count)),
  );
};

const selectGroup = async (selection: Selection): Promise<void> => {
  state.selection = selection;
  renderGroups();
  try {
    await loadChannels();
  } catch (error) {
    fail(error);
  }
};

// ---------- playlists ----------

const renderPlaylists = (): void => {
  ui.playlists.replaceChildren(
    ...state.playlists.map((p) => {
      const selected = p.id === state.playlistId;
      return h(
        'li',
        {
          class: `list-item playlist${selected ? ' selected' : ''}`,
          role: 'option',
          'aria-selected': String(selected),
          tabindex: selected ? 0 : -1,
          onclick: () => void selectPlaylist(p.id),
          onkeydown: (e: KeyboardEvent) => e.key === 'Enter' && void selectPlaylist(p.id),
        },
        h(
          'div',
          { class: 'playlist-text' },
          h('span', { class: 'item-name', title: p.name }, p.name),
          h(
            'span',
            { class: 'item-meta', title: p.source },
            `${formatCount(p.channelCount)} channels · ${p.sourceType === 'url' ? 'updated' : 'loaded'} ${timeAgo(p.lastRefreshedAt)}`,
          ),
        ),
        h(
          'span',
          { class: 'item-actions' },
          iconButton('↻', 'Refresh', () => refreshPlaylist(p)),
          iconButton('✎', 'Rename', () => renamePlaylist(p)),
          iconButton('✕', 'Delete', () => deletePlaylist(p)),
        ),
      );
    }),
  );
};

const iconButton = (icon: string, label: string, action: () => Promise<void>): HTMLButtonElement =>
  h(
    'button',
    {
      class: 'icon',
      type: 'button',
      title: label,
      'aria-label': label,
      onclick: (e: Event) => {
        e.stopPropagation();
        void action();
      },
    },
    icon,
  );

const loadPlaylists = async (): Promise<void> => {
  state.playlists = await ListPlaylists();
  renderPlaylists();
};

const selectPlaylist = async (id: number | null): Promise<void> => {
  state.playlistId = id;
  state.selection = { kind: 'all' };
  state.groups = [];
  ui.search.disabled = id === null;
  try {
    if (id === null) localStorage.removeItem(LAST_PLAYLIST_KEY);
    else localStorage.setItem(LAST_PLAYLIST_KEY, String(id));
  } catch {
    // Storage is a convenience only.
  }
  renderPlaylists();
  try {
    if (id !== null) state.groups = await ListGroups(id);
    renderGroups();
    await loadChannels();
  } catch (error) {
    fail(error);
  }
};

/** Runs an import with progress in the status bar; one at a time. */
const runImport = async (label: string, task: () => Promise<main.ImportResult>): Promise<main.ImportResult | null> => {
  if (state.importing) {
    toast('Another import is running.');
    return null;
  }
  state.importing = true;
  ui.add.disabled = true;
  ui.cancel.hidden = false;
  setStatus(`${label}…`);
  const stop = EventsOn('import-progress', (n: number) => setStatus(`${label}… ${formatCount(n)} channels`));
  try {
    return await task();
  } catch (error) {
    fail(error);
    setStatus('');
    return null;
  } finally {
    stop();
    state.importing = false;
    ui.add.disabled = false;
    ui.cancel.hidden = true;
  }
};

const describeImport = (r: main.ImportResult): string =>
  `${formatCount(r.imported)} channels imported` +
  (r.skipped ? ` · ${formatCount(r.skipped)} skipped (unsupported or unsafe stream URLs)` : '');

const addPlaylist = async (): Promise<void> => {
  const name = h('input', { type: 'text', placeholder: 'Optional — e.g. My provider', autocomplete: 'off' });
  const url = h('input', {
    type: 'url',
    placeholder: 'https://provider.example/get.php?username=…',
    autocomplete: 'off',
    spellcheck: false,
  });
  const choice = await showDialog(
    'Add playlist',
    [
      h('label', null, 'Name', name),
      h('label', null, 'Playlist URL', url),
      h('p', { class: 'hint' }, 'Or load an .m3u / .m3u8 file from this computer.'),
      h('p', { class: 'hint' }, 'The URL is stored unencrypted on this computer.'),
    ],
    [
      { label: 'Cancel', value: '' },
      { label: 'Choose file…', value: 'file' },
      { label: 'Add URL', value: 'url', kind: 'primary' },
    ],
    (value) => {
      if (value === 'url' && !url.value.trim()) {
        url.setCustomValidity('Enter a playlist URL, or choose a file instead.');
        url.reportValidity();
        url.setCustomValidity('');
        return false;
      }
      return true;
    },
  );
  if (!choice) return;

  const result = await runImport('Importing playlist', () =>
    choice === 'url' ? AddPlaylistURL(name.value, url.value.trim()) : AddPlaylistFile(name.value),
  );
  if (!result?.playlist) {
    if (result) setStatus(''); // file dialog cancelled
    return;
  }
  setStatus(`Added “${result.playlist.name}”: ${describeImport(result)}`);
  await loadPlaylists();
  await selectPlaylist(result.playlist.id);
};

const refreshPlaylist = async (p: main.Playlist, force = false): Promise<void> => {
  const result = await runImport(`Refreshing “${p.name}”`, () => RefreshPlaylist(p.id, force));
  if (!result) return;
  if (result.tooSoonMinutes > 0) {
    setStatus('');
    const ok = await confirmDialog(
      'Refresh anyway?',
      `“${p.name}” was refreshed recently. Many providers block accounts that download the playlist too often. ` +
        `It can be refreshed normally in about ${result.tooSoonMinutes} minute${result.tooSoonMinutes === 1 ? '' : 's'}.`,
      'Refresh anyway',
      'danger',
    );
    if (ok) await refreshPlaylist(p, true);
    return;
  }
  setStatus(result.notModified ? `“${p.name}” is already up to date` : `Refreshed “${p.name}”: ${describeImport(result)}`);
  await loadPlaylists();
  if (state.playlistId === p.id && !result.notModified) {
    // Channel IDs change on refresh, so reload everything for this playlist.
    const selection = state.selection;
    state.groups = await ListGroups(p.id);
    const stillExists = selection.kind !== 'group' || state.groups.some((g) => g.name === selection.name);
    state.selection = stillExists ? selection : { kind: 'all' };
    renderGroups();
    await loadChannels();
  }
};

const renamePlaylist = async (p: main.Playlist): Promise<void> => {
  const name = await promptDialog('Rename playlist', 'Name', p.name);
  if (!name || name === p.name) return;
  try {
    await RenamePlaylist(p.id, name);
    await loadPlaylists();
  } catch (error) {
    fail(error);
  }
};

const deletePlaylist = async (p: main.Playlist): Promise<void> => {
  const ok = await confirmDialog(
    'Delete playlist?',
    `“${p.name}” and its favorites will be removed from this app. This cannot be undone.`,
    'Delete',
    'danger',
  );
  if (!ok) return;
  try {
    await DeletePlaylist(p.id);
    await loadPlaylists();
    if (state.playlistId === p.id) await selectPlaylist(state.playlists[0]?.id ?? null);
    setStatus(`Deleted “${p.name}”`);
  } catch (error) {
    fail(error);
  }
};

// ---------- settings ----------

const openSettings = async (): Promise<void> => {
  let settings: main.Settings;
  try {
    settings = await GetSettings();
  } catch (error) {
    fail(error);
    return;
  }
  const vlcStatus = h('p', { class: 'vlc-status' });
  const reset = h('button', { type: 'button', class: 'small' }, 'Use auto-detect');
  const renderVLC = (s: main.Settings) => {
    vlcStatus.className = `vlc-status ${s.vlcPath ? 'ok' : 'error'}`;
    vlcStatus.textContent = s.vlcPath
      ? `${s.vlcOverride ? 'Chosen' : 'Detected'}: ${s.vlcPath}`
      : s.vlcError || 'VLC not found';
    reset.hidden = !s.vlcOverride;
  };
  const updateVLC = async (action: () => Promise<main.Settings>) => {
    try {
      settings = await action();
      renderVLC(settings);
    } catch (error) {
      fail(error);
    }
  };
  reset.addEventListener('click', () => void updateVLC(ResetVLC));
  renderVLC(settings);

  const userAgent = h('input', {
    type: 'text',
    value: settings.userAgent,
    placeholder: settings.defaultAgent,
    autocomplete: 'off',
    spellcheck: false,
  });

  await showDialog(
    'Settings',
    [
      h(
        'fieldset',
        null,
        h('legend', null, 'VLC'),
        vlcStatus,
        h(
          'div',
          { class: 'row' },
          h('button', { type: 'button', class: 'small', onclick: () => void updateVLC(ChooseVLC) }, 'Choose VLC…'),
          reset,
        ),
      ),
      h(
        'fieldset',
        null,
        h('legend', null, 'Playlist downloads'),
        h('label', null, 'User-Agent', userAgent),
        h('p', { class: 'hint' }, 'Some providers only accept specific players. Leave empty for the default.'),
      ),
      h('p', { class: 'hint' }, `IPTV Client ${settings.version} · Data folder: ${settings.dataDir}`),
    ],
    [
      { label: 'Cancel', value: '' },
      { label: 'Save', value: 'save', kind: 'primary' },
    ],
    async (value) => {
      if (value !== 'save') return true;
      try {
        await SetUserAgent(userAgent.value);
        return true;
      } catch (error) {
        fail(error);
        return false;
      }
    },
  );
};

// ---------- wiring ----------

const onSearch = debounce(async () => {
  try {
    await loadChannels();
  } catch (error) {
    fail(error);
  }
}, 200);

ui.search.addEventListener('input', () => {
  state.search = ui.search.value.trim();
  onSearch();
});
ui.search.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && ui.search.value) {
    ui.search.value = '';
    state.search = '';
    onSearch();
  } else if (e.key === 'ArrowDown' || e.key === 'Enter') {
    e.preventDefault();
    list.focus();
  }
});
ui.add.addEventListener('click', () => void addPlaylist());
ui.cancel.addEventListener('click', () => void CancelImport());
$('#settings-button').addEventListener('click', () => void openSettings());

document.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f' && !ui.search.disabled) {
    e.preventDefault();
    ui.search.focus();
    ui.search.select();
  }
});

// Disable the webview's default context menu (Reload, Inspect, ...), except
// in text fields where cut/copy/paste is useful.
document.addEventListener('contextmenu', (e) => {
  if (!(e.target instanceof HTMLInputElement)) e.preventDefault();
});

const start = async (): Promise<void> => {
  try {
    await loadPlaylists();
  } catch (error) {
    fail(error);
    renderEmpty();
    return;
  }
  let last: number | null = null;
  try {
    last = Number(localStorage.getItem(LAST_PLAYLIST_KEY)) || null;
  } catch {
    // ignore
  }
  const initial = state.playlists.find((p) => p.id === last) ?? state.playlists[0];
  await selectPlaylist(initial?.id ?? null);
};

void start();
