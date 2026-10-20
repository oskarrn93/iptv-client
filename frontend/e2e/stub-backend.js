// In-memory stand-in for the Go backend (window.go.main.App) and the Wails
// runtime, so the built frontend can be tested in a normal browser. It
// mirrors the behaviour of app.go closely enough for UI tests; the real Go
// code has its own unit tests. Loaded as a classic script before the app.
// A script loaded first can set window.__stubSeed(addPlaylist) to replace the
// test data; demo-data.js does this for the README screenshots.
(() => {
  const groupsOf = (n) => n % 120;
  const channels = new Map(); // id -> {id, playlistId, name, group, tvgId}
  const favorites = new Set(); // `${playlistId}|${tvgId}|${name}`
  const playlists = [];
  let nextId = 1;
  let nextPlaylist = 1;
  const listeners = new Map();
  const now = () => Math.floor(Date.now() / 1000);
  const fold = (s) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
  const favKey = (c) => `${c.playlistId}|${c.tvgId}|${c.name.toLowerCase()}`;

  // entries: [{name, group, tvgId, favorite?}]
  const addPlaylist = (name, sourceType, source, entries) => {
    const id = nextPlaylist++;
    for (const { favorite, ...e } of entries) {
      const cid = nextId++;
      const c = { id: cid, playlistId: id, position: cid, ...e };
      channels.set(cid, c);
      if (favorite) favorites.add(favKey(c));
    }
    const p = {
      id,
      name,
      sourceType,
      source,
      channelCount: entries.length,
      lastRefreshedAt: now(),
      guideSource: '',
      guideCustom: false,
      guideUpdatedAt: 0,
    };
    playlists.push(p);
    return p;
  };

  // Hourly programmes for channels with a tvg-id once the playlist's guide
  // was loaded. The xss channel's titles carry markup on purpose.
  const guideFor = (c) => {
    const p = playlists.find((x) => x.id === c.playlistId);
    if (!p?.guideSource || !p.guideUpdatedAt || !c.tvgId) return [];
    const hour = 3600;
    const base = Math.floor(now() / hour) * hour;
    const title = c.tvgId === 'xss' ? '<img src=x onerror="window.__xss=2"><b>Evil Show</b>' : `Show ${c.id % 7}`;
    return [0, 1, 2, 3].map((i) => ({
      title: i ? `${title} (part ${i + 1})` : title,
      description: i ? '' : `All about ${title}.`,
      start: base + i * hour,
      stop: base + (i + 1) * hour,
    }));
  };

  if (window.__stubSeed) {
    window.__stubSeed(addPlaylist);
  } else {
    const big = [
      { name: '<img src=x onerror="window.__xss=1">Evil Channel', group: '<b>Bold Group</b>', tvgId: 'xss' },
      { name: 'Mux Test Stream', group: 'Test', tvgId: '' },
    ];
    for (let i = 1; i <= 50000; i++) {
      big.push({ name: `Kanal ${i} HD Ünïcödé`, group: `Group ${groupsOf(i)} Sport & Nyheter`, tvgId: `c${i}` });
    }
    const bigList = addPlaylist('Big Test List', 'url', 'http://provider.example/…', big);
    Object.assign(bigList, { guideSource: 'http://epg.example/…', guideUpdatedAt: now() });
    addPlaylist('Small List', 'file', 'C:\\lists\\small.m3u', [
      { name: 'BBC One HD', group: 'UK', tvgId: 'bbc1' },
      { name: 'BBC One SD', group: 'UK', tvgId: 'bbc1' },
      { name: 'CNN', group: 'News', tvgId: 'cnn' },
    ]);
  }

  const byPlaylist = (pid) => [...channels.values()].filter((c) => c.playlistId === pid);
  const delay = (ms = 5) => new Promise((r) => setTimeout(r, ms));
  const emit = (name, ...args) => (listeners.get(name) ?? []).forEach((cb) => cb(...args));
  const playlist = (id) => {
    const p = playlists.find((x) => x.id === id);
    if (!p) throw 'playlist not found';
    return p;
  };

  window.__calls = [];
  window.__logs = [];
  const api = {
    async ListPlaylists() {
      return [...playlists].sort((a, b) => a.name.localeCompare(b.name));
    },
    async ListGroups(pid) {
      const groups = new Map();
      for (const c of byPlaylist(pid)) groups.set(c.group, (groups.get(c.group) ?? 0) + 1);
      return [...groups].map(([name, count]) => ({ name, count }));
    },
    async ChannelIDs(q) {
      const terms = fold(q.search).split(/\s+/).filter(Boolean);
      return byPlaylist(q.playlistId)
        .filter((c) => q.allGroups || c.group === q.group)
        .filter((c) => !q.favoritesOnly || favorites.has(favKey(c)))
        .filter((c) => {
          const words = fold(`${c.name} ${c.group}`).split(/[^\p{L}\p{N}]+/u);
          return terms.every((t) => words.some((w) => w.startsWith(t)));
        })
        .map((c) => c.id);
    },
    async Channels(ids) {
      if (ids.length > 500) throw 'at most 500 ids per request';
      await delay();
      return ids
        .map((id) => channels.get(id))
        .filter(Boolean)
        .map((c) => {
          const [current] = guideFor(c);
          return {
            id: c.id,
            name: c.name,
            group: c.group,
            favorite: favorites.has(favKey(c)),
            logo: c.tvgId !== '',
            now: current && { ...current, description: '' },
          };
        });
    },
    async ChannelGuide(id) {
      const c = channels.get(id);
      if (!c) throw 'channel not found';
      await delay();
      return guideFor(c);
    },
    async SetPlaylistGuide(id, url) {
      const p = playlist(id);
      url = url.trim();
      if (url && !/^https?:\/\/./i.test(url)) throw 'the program guide URL must be an http:// or https:// address';
      const redacted = (u) => u.replace(/^(https?:\/\/[^/?#]*)[/?#].+$/i, '$1/…');
      Object.assign(p, { guideSource: url ? redacted(url) : '', guideCustom: !!url, guideUpdatedAt: 0 });
      window.__calls.push(['SetPlaylistGuide', id, url]);
      return p;
    },
    async RefreshGuide(id) {
      const p = playlist(id);
      window.__calls.push(['RefreshGuide', id]);
      if (!p.guideSource) throw 'this playlist has no program guide URL';
      for (const n of [5000, 10000]) {
        await delay(20);
        emit('import-progress', n);
      }
      p.guideUpdatedAt = now();
      const matched = byPlaylist(id).filter((c) => c.tvgId).length;
      return { playlist: p, channels: matched, programmes: matched * 4 };
    },
    async SetFavorite(id, on) {
      const c = channels.get(id);
      if (on) favorites.add(favKey(c));
      else favorites.delete(favKey(c));
    },
    async Play(id) {
      window.__calls.push(['Play', id]);
      return 'VLC';
    },
    async CopyStreamURL(id) {
      window.__calls.push(['CopyStreamURL', id]);
    },
    async RenamePlaylist(id, name) {
      playlist(id).name = name;
    },
    async DeletePlaylist(id) {
      playlists.splice(playlists.indexOf(playlist(id)), 1);
      for (const c of byPlaylist(id)) channels.delete(c.id);
    },
    async AddPlaylistURL(name, url) {
      if (!/^https?:\/\//i.test(url)) throw 'only http:// and https:// playlist URLs are supported';
      for (const n of [1000, 2000]) {
        await delay(50);
        emit('import-progress', n);
      }
      const entries = Array.from({ length: 2500 }, (_, i) => ({ name: `New ${i}`, group: 'Imported', tvgId: '' }));
      const p = addPlaylist(name || 'provider.example', 'url', 'http://provider.example/…', entries);
      return { playlist: p, imported: 2500, skipped: 3, notModified: false, tooSoonMinutes: 0 };
    },
    async AddPlaylistFile() {
      return { playlist: null, imported: 0, skipped: 0, notModified: false, tooSoonMinutes: 0 }; // cancelled
    },
    async RefreshPlaylist(id, force) {
      const p = playlist(id);
      window.__calls.push(['RefreshPlaylist', id, force]);
      if (p.sourceType === 'url' && !force) return { playlist: null, imported: 0, skipped: 0, notModified: false, tooSoonMinutes: 42 };
      p.lastRefreshedAt = now();
      return { playlist: p, imported: p.channelCount, skipped: 0, notModified: false, tooSoonMinutes: 0 };
    },
    async SetPlaylistURL(id, url) {
      const p = playlist(id);
      url = url.trim();
      window.__calls.push(['SetPlaylistURL', id, url]);
      if (!/^https?:\/\/./i.test(url)) throw 'the playlist URL must be an http:// or https:// address';
      if (p.sourceType !== 'url') throw 'this playlist is loaded from a file, not a URL';
      for (const n of [1000, 2000]) {
        await delay(20);
        emit('import-progress', n);
      }
      Object.assign(p, { source: url.replace(/^(https?:\/\/[^/?#]*)[/?#].+$/i, '$1/…'), lastRefreshedAt: now() });
      return { playlist: p, imported: p.channelCount, skipped: 0, notModified: false, tooSoonMinutes: 0 };
    },
    async CancelImport() {},
    async GetSettings() {
      return {
        playerPath: 'C:\\Program Files\\VideoLAN\\VLC\\vlc.exe',
        playerName: 'VLC',
        playerOverride: false,
        playerError: '',
        audioLanguages: window.__audioLanguages ?? 'en',
        userAgent: window.__userAgent ?? '',
        defaultAgent: 'VLC/3.0.21 LibVLC/3.0.21',
        logLevel: window.__logLevel ?? 'info',
        dataDir: 'C:\\Users\\test\\AppData\\Roaming\\IPTVClient',
        version: 'v0.0.0-test',
      };
    },
    async ChoosePlayer() {
      return api.GetSettings();
    },
    async ResetPlayer() {
      return api.GetSettings();
    },
    async SetLogLevel(level) {
      if (!['debug', 'info', 'warn', 'error'].includes(level)) throw 'invalid log level';
      window.__logLevel = level;
    },
    async OpenLogFolder() {
      window.__calls.push(['OpenLogFolder']);
    },
    // Mirrors App.Log: never fails; lines are kept for the tests.
    async Log(level, msg, attrs) {
      window.__logs.push({ level, msg, attrs });
    },
    // Mirrors player.NormalizeLanguages; "" resets to English.
    async SetAudioLanguages(langs) {
      const codes = [...new Set(langs.split(/[ ,]+/).filter(Boolean).map((c) => c.toLowerCase()))];
      if (codes.length > 10 || codes.some((c) => !/^[a-z]{2,3}$/.test(c))) throw 'invalid language code';
      window.__audioLanguages = codes.join(',') || 'en';
    },
    async SetUserAgent(ua) {
      if (/[\r\n]/.test(ua)) throw 'invalid User-Agent';
      window.__userAgent = ua;
    },
    // window.__latest sets the newest release; defaults to the running one.
    async CheckForUpdate() {
      await delay();
      const latest = window.__latest ?? 'v0.0.0-test';
      return { current: 'v0.0.0-test', latest, available: latest !== 'v0.0.0-test' };
    },
    async InstallUpdate() {
      for (const pct of [10, 50, 100]) {
        await delay();
        emit('update-progress', pct);
      }
      window.__installed = true;
    },
  };

  window.__emit = emit;
  window.go = { main: { App: api } };
  window.runtime = {
    EventsOnMultiple(name, cb) {
      const list = listeners.get(name) ?? [];
      list.push(cb);
      listeners.set(name, list);
      return () => listeners.set(name, (listeners.get(name) ?? []).filter((x) => x !== cb));
    },
    EventsOff(name) {
      listeners.delete(name);
    },
  };
})();
