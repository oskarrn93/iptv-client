import './style.css';

import {
  AddPlaylistFile,
  AddPlaylistURL,
  CancelImport,
  ChannelGuide,
  ChannelIDs,
  CheckForUpdate,
  ChoosePlayer,
  CopyStreamURL,
  DeletePlaylist,
  GetSettings,
  InstallUpdate,
  ListGroups,
  ListPlaylists,
  OpenLogFolder,
  Play,
  RefreshGuide,
  RefreshPlaylist,
  RenamePlaylist,
  ResetPlayer,
  SetFavorite,
  SetLogLevel,
  SetPlaylistGuide,
  SetAudioLanguages,
  SetUserAgent,
} from '../wailsjs/go/main/App';
import { main } from '../wailsjs/go/models';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { ChannelList } from './channel-list';
import { confirmDialog, showDialog } from './dialogs';
import {
  $,
  append,
  type Child,
  channelLogo,
  debounce,
  errorMessage,
  formatCount,
  groupLabel,
  h,
  plural,
  setSelected,
  timeAgo,
} from './dom';
import { installErrorHandlers, type Level } from './log';
import { showMenu } from './menu';

installErrorHandlers();

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
  details: $('#channel-details'),
};

// ---------- feedback ----------

const toast = (message: string, kind: 'error' | 'info' = 'error'): void => {
  const el = h('div', { class: `toast ${kind}`, role: kind === 'error' ? 'alert' : 'status' }, message);
  el.addEventListener('click', () => el.remove());
  ui.toasts.append(el);
  setTimeout(() => el.remove(), kind === 'error' ? 9000 : 4000);
};

const fail = (error: unknown): void => toast(errorMessage(error));

/** Wraps an async handler so that a rejection is shown as a toast. */
const guard =
  <A extends unknown[]>(fn: (...args: A) => Promise<void>) =>
  async (...args: A): Promise<void> => {
    try {
      await fn(...args);
    } catch (error) {
      fail(error);
    }
  };

const setStatus = (text: string): void => {
  ui.status.textContent = text;
};

// ---------- channel list ----------

const play = guard(async (channel: main.Channel) => {
  const player = await Play(channel.id);
  setStatus(`Opened “${channel.name}” in ${player}`);
});

const toggleFavorite = guard(async (channel: main.Channel) => {
  await SetFavorite(channel.id, !channel.favorite);
  if (state.selection.kind === 'favorites') {
    await loadChannels({ keepScroll: true });
  } else {
    list.update(main.Channel.createFrom({ ...channel, favorite: !channel.favorite }));
  }
});

// The URL itself is copied by Go and never shown here: it often contains
// the provider credentials.
const copyURL = guard(async (channel: main.Channel) => {
  await CopyStreamURL(channel.id);
  setStatus(`Copied the stream URL of “${channel.name}”`);
});

const modKey = /Mac/.test(navigator.userAgent) ? '⌘' : 'Ctrl+';

const list = new ChannelList($('#channels'), {
  onPlay: play,
  onToggleFavorite: toggleFavorite,
  onCopyURL: copyURL,
  onContextMenu: (channel, x, y) =>
    showMenu(x, y, [
      { label: 'Play', shortcut: 'Enter', action: () => void play(channel) },
      { label: 'Copy stream URL', shortcut: `${modKey}C`, action: () => void copyURL(channel) },
      {
        label: channel.favorite ? 'Remove from favorites' : 'Add to favorites',
        shortcut: 'F',
        action: () => void toggleFavorite(channel),
      },
    ]),
  onSelect: (channel) => showDetails(channel),
  onError: fail,
});

// ---------- selected channel details ----------

const clock = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });
const time = (unixSeconds: number): string => clock.format(unixSeconds * 1000);

let detailsChannel: main.Channel | undefined;

/** Shows the selected channel with what is on now and next. */
const showDetails = (channel: main.Channel | undefined): void => {
  detailsChannel = channel;
  ui.details.hidden = !channel;
  if (!channel) return;
  renderDetails(channel, null);
  void loadDetails(channel);
};

// Debounced: holding an arrow key selects many channels in a row.
const loadDetails = debounce(async (channel: main.Channel): Promise<void> => {
  try {
    const programmes = await ChannelGuide(channel.id);
    if (channel === detailsChannel) renderDetails(channel, programmes);
  } catch (error) {
    if (channel === detailsChannel) renderDetails(channel, []);
    fail(error);
  }
}, 150);

const renderDetails = (channel: main.Channel, programmes: main.Programme[] | null): void => {
  const now = Date.now() / 1000;
  const [first, ...rest] = programmes ?? [];
  const current = first && first.start <= now ? first : undefined;
  const next = current ? rest : (programmes ?? []);
  const playlist = state.playlists.find((p) => p.id === state.playlistId);
  let info: Child[];
  if (programmes === null) {
    info = [h('div', { class: 'muted' }, channel.now?.title ?? '…')];
  } else if (programmes.length === 0) {
    info = [
      h(
        'div',
        { class: 'muted' },
        playlist?.guideSource
          ? 'No program information for this channel.'
          : 'No program guide. Add an EPG URL with ✎ on the playlist.',
      ),
    ];
  } else {
    info = [
      current &&
        h(
          'div',
          { class: 'details-now' },
          h('span', { class: 'programme-time' }, `${time(current.start)}–${time(current.stop)}`),
          h('span', { class: 'programme-title', title: current.title }, current.title),
          h('progress', {
            max: current.stop - current.start,
            value: now - current.start,
            title: `${Math.max(1, Math.round((current.stop - now) / 60))} min left`,
          }),
        ),
      current?.description && h('p', { class: 'details-description', title: current.description }, current.description),
      next.length > 0 &&
        h(
          'div',
          { class: 'details-next' },
          ...next.map((p) => h('span', { title: p.title }, `${time(p.start)} ${p.title}`)),
        ),
    ];
  }
  ui.details.replaceChildren();
  append(
    ui.details,
    channelLogo(channel, 'details-logo'),
    h('div', { class: 'details-title', title: channel.name }, channel.name),
    ...info,
  );
};

// Programmes end: refresh what is shown every minute.
setInterval(() => {
  if (document.hidden) return;
  list.reloadIfEnded();
  if (detailsChannel) void loadDetails(detailsChannel);
}, 60_000);

const selectionLabel = (s: Selection): string =>
  s.kind === 'all' ? 'All channels' : s.kind === 'favorites' ? 'Favorites' : groupLabel(s.name);

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
  ui.count.textContent = plural(ids.length, 'channel');
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

/** A selectable listbox option with a roving tabindex. */
const listItem = (selected: boolean, extraClass: string, onActivate: () => void, ...children: Child[]): HTMLLIElement => {
  const li = h(
    'li',
    {
      class: `list-item ${extraClass}`,
      role: 'option',
      onclick: onActivate,
      onkeydown: (e: KeyboardEvent) => e.key === 'Enter' && onActivate(),
    },
    ...children,
  );
  setSelected(li, selected, true);
  return li;
};

// ---------- groups ----------

const selectionKey = (s: Selection): string => (s.kind === 'group' ? `group:${s.name}` : s.kind);

const renderGroups = (): void => {
  if (state.playlistId === null) {
    ui.groups.replaceChildren();
    return;
  }
  const total = state.groups.reduce((sum, g) => sum + g.count, 0);
  const current = selectionKey(state.selection);
  const item = (selection: Selection, label: string, count?: number, extraClass = '') => {
    const key = selectionKey(selection);
    const li = listItem(
      key === current,
      extraClass,
      () => void selectGroup(selection),
      h('span', { class: 'item-name', title: label }, label),
      count !== undefined && h('span', { class: 'item-count' }, formatCount(count)),
    );
    li.dataset.key = key;
    return li;
  };
  ui.groups.replaceChildren(
    item({ kind: 'all' }, 'All channels', total, 'special'),
    item({ kind: 'favorites' }, '★ Favorites', undefined, 'special'),
    h('li', { class: 'separator', role: 'presentation' }),
    ...state.groups.map((g) => item({ kind: 'group', name: g.name }, groupLabel(g.name), g.count)),
  );
};

const selectGroup = guard(async (selection: Selection) => {
  state.selection = selection;
  // Restyle rather than rebuild: a big provider can have thousands of groups.
  const current = selectionKey(selection);
  for (const li of ui.groups.querySelectorAll<HTMLElement>('li[data-key]')) {
    setSelected(li, li.dataset.key === current, true);
  }
  await loadChannels();
});

// ---------- playlists ----------

const renderPlaylists = (): void => {
  ui.playlists.replaceChildren(
    ...state.playlists.map((p) =>
      listItem(
        p.id === state.playlistId,
        'playlist',
        () => void selectPlaylist(p.id),
        h(
          'div',
          { class: 'playlist-text' },
          h('span', { class: 'item-name', title: p.name }, p.name),
          h(
            'span',
            { class: 'item-meta', title: p.source },
            `${plural(p.channelCount, 'channel')} · ${p.sourceType === 'url' ? 'updated' : 'loaded'} ${timeAgo(p.lastRefreshedAt)}`,
          ),
        ),
        h(
          'span',
          { class: 'item-actions' },
          iconButton('↻', 'Refresh', () => refreshPlaylist(p)),
          iconButton('✎', 'Edit', () => editPlaylist(p)),
          iconButton('✕', 'Delete', () => deletePlaylist(p)),
        ),
      ),
    ),
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

const selectPlaylist = guard(async (id: number | null) => {
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
  // Groups and channel IDs are independent full-playlist queries.
  const [groups] = await Promise.all([id === null ? [] : ListGroups(id), loadChannels()]);
  if (id !== state.playlistId) return; // the user moved on
  state.groups = groups;
  renderGroups();
  const p = state.playlists.find((x) => x.id === id);
  if (p && guideStale(p) && !guideTried.has(p.id) && !state.importing) {
    guideTried.add(p.id);
    await refreshGuide(p);
  }
});

// ---------- program guide ----------

const GUIDE_MAX_AGE = 12 * 3600; // seconds

const guideStale = (p: main.Playlist): boolean =>
  p.guideSource !== '' && Date.now() / 1000 - p.guideUpdatedAt > GUIDE_MAX_AGE;

// Playlists whose stale guide was refreshed automatically this session, so
// a failing guide is not retried (with an error) on every visit.
const guideTried = new Set<number>();

const refreshGuide = async (p: main.Playlist): Promise<void> => {
  const result = await runImport(`Updating the program guide of “${p.name}”`, () => RefreshGuide(p.id), 'programme');
  if (!result) return;
  setStatus(
    `Updated the program guide of “${p.name}”: ${plural(result.programmes, 'programme')} ` +
      `for ${plural(result.channels, 'channel')}`,
  );
  await loadPlaylists();
  if (state.playlistId === p.id) {
    list.reload();
    showDetails(detailsChannel);
  }
};

/** Runs an import with progress in the status bar; one at a time. */
const runImport = async <T>(label: string, task: () => Promise<T>, unit = 'channel'): Promise<T | null> => {
  if (state.importing) {
    toast('Another import is running.');
    return null;
  }
  state.importing = true;
  ui.add.disabled = true;
  ui.cancel.hidden = false;
  setStatus(`${label}…`);
  const stop = EventsOn('import-progress', (n: number) => setStatus(`${label}… ${plural(n, unit)}`));
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
  `${plural(r.imported, 'channel')} imported` +
  (r.skipped ? ` · ${formatCount(r.skipped)} skipped (unsupported or unsafe stream URLs)` : '');

const addPlaylist = async (): Promise<void> => {
  const name = h('input', { type: 'text', placeholder: 'Optional — e.g. My provider', autocomplete: 'off' });
  const url = h('input', {
    type: 'url',
    placeholder: 'https://provider.example/get.php?username=…',
    autocomplete: 'off',
    spellcheck: false,
  });
  const guide = guideInput();
  const choice = await showDialog(
    'Add playlist',
    [
      h('label', null, 'Name', name),
      h('label', null, 'Playlist URL', url),
      h('p', { class: 'hint' }, 'Or load an .m3u / .m3u8 file from this computer.'),
      h('label', null, 'Program guide (EPG) URL', guide),
      h('p', { class: 'hint' }, 'Optional: an XMLTV guide, if the playlist doesn’t name one.'),
      h('p', { class: 'hint' }, 'URLs are stored unencrypted on this computer.'),
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
      return checkGuideURL(guide);
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
  if (guide.value.trim()) {
    try {
      await SetPlaylistGuide(result.playlist.id, guide.value.trim());
    } catch (error) {
      fail(error);
    }
  }
  await loadPlaylists();
  await selectPlaylist(result.playlist.id); // also loads the guide
};

const guideInput = (): HTMLInputElement =>
  h('input', {
    type: 'url',
    placeholder: 'https://provider.example/xmltv.php?…  (.xml or .xml.gz)',
    autocomplete: 'off',
    spellcheck: false,
  });

const checkGuideURL = (input: HTMLInputElement): boolean => {
  const value = input.value.trim();
  if (!value || /^https?:\/\/./i.test(value)) return true;
  input.setCustomValidity('Enter an http:// or https:// address.');
  input.reportValidity();
  input.setCustomValidity('');
  return false;
};

const refreshPlaylist = async (p: main.Playlist, force = false): Promise<void> => {
  const result = await runImport(`Refreshing “${p.name}”`, () => RefreshPlaylist(p.id, force));
  if (!result) return;
  if (result.tooSoonMinutes > 0) {
    setStatus('');
    const ok = await confirmDialog(
      'Refresh anyway?',
      `“${p.name}” was refreshed recently. Many providers block accounts that download the playlist too often. ` +
        `It can be refreshed normally in about ${plural(result.tooSoonMinutes, 'minute')}.`,
      'Refresh anyway',
      'danger',
    );
    if (ok) await refreshPlaylist(p, true);
    return;
  }
  setStatus(result.notModified ? `“${p.name}” is already up to date` : `Refreshed “${p.name}”: ${describeImport(result)}`);
  // Channel IDs change on refresh, so reload everything for this playlist.
  const reload = state.playlistId === p.id && !result.notModified;
  const [, groups] = await Promise.all([loadPlaylists(), reload ? ListGroups(p.id) : null]);
  if (groups && state.playlistId === p.id) {
    const selection = state.selection;
    state.groups = groups;
    const stillExists = selection.kind !== 'group' || state.groups.some((g) => g.name === selection.name);
    state.selection = stillExists ? selection : { kind: 'all' };
    renderGroups();
    await loadChannels();
  }
  // A refresh asks for fresh data, and the playlist may name a new guide.
  // An unchanged playlist can't, and its channels still match the guide.
  const updated = state.playlists.find((x) => x.id === p.id);
  if (updated?.guideSource && (!result.notModified || guideStale(updated))) await refreshGuide(updated);
};

const editPlaylist = guard(async (p: main.Playlist) => {
  const name = h('input', { type: 'text', value: p.name, required: true, autocomplete: 'off', spellcheck: false });
  const guide = guideInput();
  let useDefault = false;
  const guideStatus = h('p', { class: 'hint' });
  const reset = h('button', { type: 'button', class: 'small' }, 'Use the playlist’s guide');
  const renderGuide = () => {
    // Only the redacted URL is known here: it may contain credentials.
    guideStatus.textContent = useDefault
      ? 'The guide named in the playlist (if any) will be used.'
      : p.guideCustom
        ? `Current guide: ${p.guideSource}. Enter a new URL to replace it.`
        : p.guideSource
          ? `The playlist names a guide: ${p.guideSource}. Enter a URL to use another.`
          : 'No guide yet. Enter an XMLTV URL to show what is on.';
    reset.hidden = !p.guideCustom || useDefault;
  };
  reset.addEventListener('click', () => {
    useDefault = true;
    guide.value = '';
    renderGuide();
  });
  renderGuide();

  let reload = false;
  const choice = await showDialog(
    'Edit playlist',
    [
      h('label', null, 'Name', name),
      h('label', null, 'Program guide (EPG) URL', guide),
      guideStatus,
      h('div', { class: 'row' }, reset),
    ],
    [
      { label: 'Cancel', value: '' },
      { label: 'Save', value: 'save', kind: 'primary' },
    ],
    async () => {
      if (!checkGuideURL(guide)) return false;
      try {
        const newName = name.value.trim();
        if (newName && newName !== p.name) await RenamePlaylist(p.id, newName);
        const url = guide.value.trim();
        if (url || useDefault) {
          const updated = await SetPlaylistGuide(p.id, url);
          reload = updated.guideSource !== '';
        }
        return true;
      } catch (error) {
        fail(error);
        return false;
      }
    },
  );
  if (!choice) return;
  await loadPlaylists();
  const updated = state.playlists.find((x) => x.id === p.id);
  if (reload && updated) await refreshGuide(updated);
  else if (state.playlistId === p.id) showDetails(detailsChannel);
});

const deletePlaylist = guard(async (p: main.Playlist) => {
  const ok = await confirmDialog(
    'Delete playlist?',
    `“${p.name}” and its favorites will be removed from this app. This cannot be undone.`,
    'Delete',
    'danger',
  );
  if (!ok) return;
  await DeletePlaylist(p.id);
  await loadPlaylists();
  if (state.playlistId === p.id) await selectPlaylist(state.playlists[0]?.id ?? null);
  setStatus(`Deleted “${p.name}”`);
});

// ---------- settings ----------

/** "Check for updates…": finds a newer release and offers to install it. */
const updateButton = (current: string): HTMLButtonElement => {
  const label = 'Check for updates…';
  const button = h('button', { type: 'button', class: 'small' }, label);
  const busy = (text: string) => {
    button.disabled = true;
    button.textContent = text;
  };
  const done = () => {
    button.disabled = false;
    button.textContent = label;
  };
  let restarting = false;
  button.addEventListener('click', async () => {
    busy('Checking…');
    try {
      const info = await CheckForUpdate();
      if (!info.available) {
        toast(`You have the latest version (${current}).`, 'info');
        return;
      }
      const ok = await confirmDialog(
        `Update to ${info.latest}?`,
        `IPTV Client ${info.latest} is available (you have ${current}).` +
          'It will be downloaded and installed, and the app will restart.',
        'Install and restart',
      );
      if (!ok) return;
      busy('Downloading…');
      const stop = EventsOn('update-progress', (pct: number) => busy(`Downloading… ${pct}%`));
      try {
        await InstallUpdate();
        restarting = true;
        busy('Restarting…');
      } finally {
        stop();
      }
    } catch (error) {
      fail(error);
    } finally {
      if (!restarting) done();
    }
  });
  return button;
};

const openLogFolder = guard(() => OpenLogFolder());

const logFolderButton = (): HTMLButtonElement =>
  h('button', { type: 'button', class: 'small', onclick: () => void openLogFolder() }, 'Open log folder…');

const openSettings = async (): Promise<void> => {
  let settings: main.Settings;
  try {
    settings = await GetSettings();
  } catch (error) {
    // Usually the database failed to open; the log says why.
    await showDialog(
      'Settings unavailable',
      [h('p', null, errorMessage(error)), h('div', { class: 'row' }, logFolderButton())],
      [{ label: 'Close', value: '' }],
    );
    return;
  }
  const playerStatus = h('p', { class: 'player-status' });
  const reset = h('button', { type: 'button', class: 'small' }, 'Use VLC (auto-detect)');
  const renderPlayer = (s: main.Settings) => {
    playerStatus.className = `player-status ${s.playerPath ? 'ok' : 'error'}`;
    playerStatus.textContent = s.playerPath
      ? `${s.playerOverride ? 'Chosen' : 'Detected'} ${s.playerName}: ${s.playerPath}`
      : s.playerError || 'VLC not found';
    reset.hidden = !s.playerOverride;
  };
  const updatePlayer = guard(async (action: () => Promise<main.Settings>) => {
    settings = await action();
    renderPlayer(settings);
  });
  reset.addEventListener('click', () => void updatePlayer(ResetPlayer));
  renderPlayer(settings);

  const audioLanguages = h('input', {
    id: 'audio-languages',
    type: 'text',
    value: settings.audioLanguages,
    placeholder: 'en',
    autocomplete: 'off',
    spellcheck: false,
  });

  const userAgent = h('input', {
    id: 'user-agent',
    type: 'text',
    value: settings.userAgent,
    placeholder: settings.defaultAgent,
    autocomplete: 'off',
    spellcheck: false,
  });

  const levels: [Level, string][] = [
    ['debug', 'Debug (until restart)'],
    ['info', 'Info'],
    ['warn', 'Warning'],
    ['error', 'Error'],
  ];
  const logLevel = h(
    'select',
    { id: 'log-level' },
    ...levels.map(([value, label]) => h('option', { value, selected: value === settings.logLevel }, label)),
  );

  await showDialog(
    'Settings',
    [
      h(
        'fieldset',
        null,
        h('legend', null, 'Player'),
        playerStatus,
        h(
          'div',
          { class: 'row' },
          h('button', { type: 'button', class: 'small', onclick: () => void updatePlayer(ChoosePlayer) }, 'Choose player…'),
          reset,
        ),
        h(
          'p',
          { class: 'hint' },
          'VLC is used if installed. You can also choose mpv, MPC-HC, PotPlayer or IINA; ' +
            'MPC-HC and PotPlayer ignore the playlist’s User-Agent and Referrer.',
        ),
        h('label', null, 'Preferred audio languages', audioLanguages),
        h(
          'p',
          { class: 'hint' },
          'Language codes, most preferred first, e.g. “sv, en, deu”. The player picks the first ' +
            'language a channel has, otherwise its default track. MPC-HC and PotPlayer ignore this.',
        ),
      ),
      h(
        'fieldset',
        null,
        h('legend', null, 'Playlist downloads'),
        h('label', null, 'User-Agent', userAgent),
        h('p', { class: 'hint' }, 'Some providers only accept specific players. Leave empty for the default.'),
      ),
      h(
        'fieldset',
        null,
        h('legend', null, 'Logging'),
        h('label', null, 'Log level', logLevel),
        h('div', { class: 'row' }, logFolderButton()),
        h(
          'p',
          { class: 'hint' },
          'Use Debug when reporting a problem. Logs never contain stream or playlist URLs.',
        ),
      ),
      h(
        'div',
        { class: 'row version-row' },
        h('p', { class: 'hint' }, `IPTV Client ${settings.version} · Data folder: ${settings.dataDir}`),
        updateButton(settings.version),
      ),
    ],
    [
      { label: 'Cancel', value: '' },
      { label: 'Save', value: 'save', kind: 'primary' },
    ],
    async (value) => {
      if (value !== 'save') return true;
      try {
        await SetAudioLanguages(audioLanguages.value);
        await SetUserAgent(userAgent.value);
        await SetLogLevel(logLevel.value);
        return true;
      } catch (error) {
        fail(error);
        return false;
      }
    },
  );
};

// ---------- wiring ----------

// One search query at a time: on a big playlist a query can outlast the
// debounce, and starting another per keystroke only wastes work. Input that
// arrives meanwhile is searched once the running query finishes, even if
// that query failed.
let searching = false;
let searchAgain = false;
const search = async (): Promise<void> => {
  if (searching) {
    searchAgain = true;
    return;
  }
  searching = true;
  do {
    searchAgain = false;
    try {
      await loadChannels();
    } catch (error) {
      fail(error);
    }
  } while (searchAgain);
  searching = false;
};
const onSearch = debounce(search, 200);

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
