// UI tests for the built frontend (frontend/dist, including its production
// CSP) against an in-memory stub of the Go API (stub-backend.js).
//
//   npm run test:e2e                 # uses Microsoft Edge (Windows default)
//   E2E_BROWSER=chrome npm run test:e2e
//   E2E_SCREENSHOTS=dir npm run test:e2e
//
// Uses an installed Edge/Chrome via playwright-core; no browser download.
import { join } from 'node:path';
import { chromium } from 'playwright-core';
import { serve } from './serve.mjs';

const shots = process.env.E2E_SCREENSHOTS;
const server = await serve(['stub-backend.js']);
const url = server.url;

const results = [];
const check = (name, ok, detail = '') => {
  results.push(ok);
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  (${detail})` : ''}`);
};

const browser = await chromium.launch({ channel: process.env.E2E_BROWSER ?? 'msedge', headless: true });
const page = await browser.newPage({ viewport: { width: 1200, height: 760 } });
const problems = [];
page.on('console', (m) => ['error', 'warning'].includes(m.type()) && problems.push(`${m.type()}: ${m.text()}`));
page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
const shot = async (name) => shots && page.screenshot({ path: join(shots, `${name}.png`) });
// Playwright's waitForFunction evaluates strings in the page, which the
// production CSP (correctly) forbids; poll with evaluate() instead.
const waitFor = async (fn, arg, timeout = 10000) => {
  const end = Date.now() + timeout;
  while (!(await page.evaluate(fn, arg))) {
    if (Date.now() > end) throw new Error(`timed out waiting for ${fn}`);
    await new Promise((r) => setTimeout(r, 25));
  }
};
const count = () => page.textContent('#channels-count');
const titleIs = (t) => waitFor((t) => document.querySelector('#channels-title')?.textContent === t, t);

try {
  await page.goto(url);
  await waitFor(() => document.querySelector('#channels-count')?.textContent?.includes('channel'));
  await shot('01-initial');

  const csp = await page.evaluate(() => document.querySelector('meta[http-equiv="Content-Security-Policy"]')?.content ?? '');
  check('production CSP present', csp.includes("script-src 'self'") && csp.includes("object-src 'none'"));

  const names = await page.$$eval('#playlists .item-name', (els) => els.map((e) => e.textContent));
  check('playlists listed alphabetically', names.join('|') === 'Big Test List|Small List', names.join('|'));
  check('all channels count', /^50\D?002 channels$/.test(await count()), await count());

  // Untrusted names render as text.
  const evil = await page.$$eval('.channel-name', (els) => els.map((e) => e.textContent).find((t) => t.includes('Evil')));
  check('HTML in channel name shown as text', evil?.startsWith('<img src=x'), evil);
  check(
    'no script ran from playlist data',
    await page.evaluate(
      () =>
        window.__xss === undefined &&
        [...document.querySelectorAll('#channels img')].every((i) => /^\/logo\/\d+$/.test(i.getAttribute('src') ?? '')),
    ),
  );
  const bold = await page.$$eval('#groups .item-name', (els) => els.map((e) => e.textContent));
  check('HTML in group name shown as text', bold.includes('<b>Bold Group</b>'));

  // Logos are served by the app, never loaded from the playlist's URLs.
  await waitFor(() => document.querySelector('.channel-row img.channel-logo')?.complete);
  const logos = await page.$$eval('.channel-row img.channel-logo', (els) => els.map((e) => e.getAttribute('src')));
  check('channel logos load from the app', logos.length > 5 && logos.every((src) => /^\/logo\/\d+$/.test(src)), logos[0]);
  check(
    'logos render',
    await page.evaluate(() => [...document.querySelectorAll('img.channel-logo')].every((i) => i.naturalWidth > 0)),
  );

  // Program guide: on now in the list, now and next for the selection.
  const onNow = await page.$$eval('.channel-now', (els) => els.map((e) => e.textContent));
  check('list shows what is on', onNow.includes('<img src=x onerror="window.__xss=2"><b>Evil Show</b>') && onNow.includes('Show 3'), onNow.slice(0, 3).join('|'));
  await waitFor(() => document.querySelector('#channel-details .details-next'));
  const details = await page.evaluate(() => ({
    title: document.querySelector('#channel-details .details-title')?.textContent,
    now: document.querySelector('#channel-details .programme-title')?.textContent,
    description: document.querySelector('#channel-details .details-description')?.textContent,
    next: document.querySelectorAll('#channel-details .details-next span').length,
    progress: !!document.querySelector('#channel-details progress'),
  }));
  check(
    'details show now and next as text',
    details.title.includes('Evil Channel') && details.now.startsWith('<img src=x') && details.description.startsWith('All about') &&
      details.next === 3 && details.progress,
    JSON.stringify(details),
  );
  check('no script ran from guide data', await page.evaluate(() => window.__xss === undefined && !document.querySelector('#channel-details b')));
  await shot('01b-guide');

  // Virtualization.
  const rows = await page.$$eval('.channel-row', (els) => els.length);
  check('only visible rows are in the DOM', rows > 5 && rows < 80, `${rows} rows`);
  const t0 = Date.now();
  await page.$eval('#channels', (el) => (el.scrollTop = el.scrollHeight));
  await waitFor(() =>
    [...document.querySelectorAll('.channel-row .channel-name')].some((e) => e.textContent.startsWith('Kanal 50000 ')),
  );
  check('scrolling to the end loads the last rows', true, `${Date.now() - t0} ms`);
  await shot('02-scrolled');

  // Groups.
  await page.click('#groups .list-item:has-text("Group 7 Sport")');
  await titleIs('Group 7 Sport & Nyheter');
  check('group filter', /^41[67] channels$/.test(await count()), await count());
  check('group column hidden inside a group', (await page.$$('.channel-group')).length === 0);

  // Search (Ctrl+F focuses), prefix + diacritics-insensitive.
  await page.click('#groups .list-item:has-text("All channels")');
  await titleIs('All channels');
  await page.keyboard.press('Control+f');
  check('Ctrl+F focuses search', await page.evaluate(() => document.activeElement?.id === 'search'));
  await page.keyboard.type('kanal 4999 unicode');
  await waitFor(() => document.querySelector('#channels-title')?.textContent?.endsWith('“kanal 4999 unicode”'));
  check('search', (await count()) === '11 channels', await count());
  await shot('03-search');

  // Keyboard: into the list, move, favorite, play.
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  // Selection is drawn on the next animation frame.
  await waitFor(() => {
    const row = document.querySelector('.channel-row.selected');
    return row?.dataset.index === '1' && !row.querySelector('.placeholder');
  });
  const selected = await page.textContent('.channel-row.selected .channel-name');
  await page.keyboard.press('f');
  await page.waitForSelector('.channel-row.selected .star.on');
  await page.keyboard.press('Enter');
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Opened'));
  const played = await page.evaluate(() => window.__calls.filter(([m]) => m === 'Play').length);
  const status = await page.textContent('#status');
  check('keyboard: arrows select, F favorites, Enter plays', played === 1 && status.includes(selected), selected);
  check('status names the player', status.endsWith('in VLC'), status);
  check('arrow key moved past the first result', !selected.startsWith('Kanal 4999 '), selected);

  // A human click on an unselected row's buttons: mousedown selects the row,
  // and mouseup lands frames later. The button must survive in between.
  await page.hover('.channel-row[data-index="3"] .channel-name');
  await page.click('.channel-row[data-index="3"] .play', { delay: 100, timeout: 5000 });
  await waitFor(() => window.__calls.filter(([m]) => m === 'Play').length === 2);
  check('Play button works on an unselected row', true);
  await page.hover('.channel-row[data-index="4"] .channel-name');
  await page.click('.channel-row[data-index="4"] .star', { delay: 100, timeout: 5000 });
  await page.waitForSelector('.channel-row[data-index="4"] .star.on', { timeout: 5000 });
  check('star button works on an unselected row', true);
  await page.click('.channel-row[data-index="4"] .star', { delay: 100, timeout: 5000 });
  await page.waitForSelector('.channel-row[data-index="4"] .star:not(.on)', { timeout: 5000 });
  // Back to the keyboard-selected row for the copy checks below.
  await page.focus('#channels');
  await page.keyboard.press('Home');
  await page.keyboard.press('ArrowDown');

  // Copy the stream URL: Ctrl+C on the selection, or from the context menu.
  const copies = () => page.evaluate(() => window.__calls.filter(([m]) => m === 'CopyStreamURL').map(([, id]) => id));
  await page.keyboard.press('Control+c');
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Copied'));
  check('Ctrl+C copies the stream URL', (await copies()).length === 1 && (await page.textContent('#status')).includes(selected));

  await page.click('.channel-row:nth-child(1) .channel-name', { button: 'right' });
  await page.waitForSelector('.menu[role="menu"]');
  await shot('03b-context-menu');
  const items = await page.$$eval('.menu [role="menuitem"] span', (els) => els.map((e) => e.textContent));
  check('context menu items', items.join('|') === 'Play|Copy stream URL|Add to favorites', items.join('|'));
  check('context menu has keyboard focus', await page.evaluate(() => document.activeElement?.textContent?.startsWith('Play')));
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await waitFor(() => !document.querySelector('.menu'));
  const copied = await copies();
  check('context menu copies the right-clicked channel', copied.length === 2 && copied[1] !== copied[0], copied.join());
  check('focus returns to the list', await page.evaluate(() => document.activeElement?.id === 'channels'));

  await page.keyboard.press('Shift+F10');
  await page.waitForSelector('.menu');
  await page.keyboard.press('Escape');
  await waitFor(() => !document.querySelector('.menu'));
  check('Shift+F10 opens the menu, Escape closes it', (await copies()).length === 2);

  await page.click('.channel-row:nth-child(1) .channel-name', { button: 'right' });
  await page.waitForSelector('.menu');
  await page.mouse.click(5, 5);
  await waitFor(() => !document.querySelector('.menu'));
  check('clicking elsewhere closes the menu', true);

  // Double-click plays too.
  await page.dblclick('.channel-row:nth-child(3) .channel-name', { delay: 50 });
  await waitFor(() => window.__calls.filter(([m]) => m === 'Play').length === 3);
  check('double-click plays', true);

  // Escape clears search.
  await page.focus('#search');
  await page.keyboard.press('Escape');
  await titleIs('All channels');
  check('Escape clears search', (await page.inputValue('#search')) === '');

  // Favorites view; unfavorite removes the row.
  await page.click('#groups .list-item:has-text("Favorites")');
  await titleIs('Favorites');
  await waitFor(() => document.querySelector('.channel-row .channel-name:not(.placeholder)'));
  const favs = await page.$$eval('.channel-row .channel-name', (els) => els.map((e) => e.textContent));
  check('favorites view', favs.length === 1 && favs[0] === selected, favs.join('|'));
  await page.click('.channel-row .star');
  await waitFor(() => !document.querySelector('#empty').hidden);
  check('unfavorite updates the view', (await page.textContent('#empty')).includes('No favorites'));

  // Refresh: rate-limited -> confirm -> forced.
  await page.click('#playlists .list-item.selected button[title="Refresh"]');
  await page.waitForSelector('dialog[open]');
  check('rate-limited refresh asks first', (await page.textContent('dialog[open]')).includes('42 minutes'));
  await shot('04-refresh-confirm');
  await page.click('dialog[open] button:has-text("Refresh anyway")');
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Updated the program guide'));
  const forced = await page.evaluate(() => window.__calls.filter(([m]) => m === 'RefreshPlaylist').map(([, , f]) => f));
  check('refresh anyway forces', forced.join() === 'false,true', forced.join());
  check('refresh also updates the guide', await page.evaluate(() => window.__calls.some(([m]) => m === 'RefreshGuide')));

  // Settings.
  await page.click('#settings-button');
  await page.waitForSelector('dialog[open] .player-status.ok');
  check('settings shows player', (await page.textContent('dialog[open] .player-status')).includes('Detected VLC: '));
  check('settings shows version', (await page.textContent('dialog[open]')).includes('IPTV Client v0.0.0-test'));
  check('audio languages default to English', (await page.inputValue('#audio-languages')) === 'en');
  await page.fill('#audio-languages', 'SV, en');
  await page.fill('#user-agent', 'MyPlayer/1.0');
  await shot('05-settings');
  await page.click('dialog[open] button:has-text("Save")');
  await page.waitForSelector('dialog[open]', { state: 'detached' });
  check('settings save', (await page.evaluate(() => window.__userAgent)) === 'MyPlayer/1.0');
  check('audio languages save', (await page.evaluate(() => window.__audioLanguages)) === 'sv,en');

  // Theme: previewed while choosing, reverted on cancel, saved on save.
  const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  const palette = () =>
    page.evaluate(() => {
      const style = getComputedStyle(document.documentElement);
      return [...document.styleSheets[0].cssRules[0].style].map((v) => style.getPropertyValue(v)).join();
    });
  const openTheme = async () => {
    await page.click('#settings-button');
    await page.waitForSelector('dialog[open] #theme');
  };
  const saveTheme = async (value) => {
    await openTheme();
    await page.selectOption('#theme', value);
    await page.click('dialog[open] button:has-text("Save")');
    await page.waitForSelector('dialog[open]', { state: 'detached' });
  };
  await page.emulateMedia({ colorScheme: 'dark' });
  const systemDark = await palette();
  await page.emulateMedia({ colorScheme: 'light' });
  const lightBg = await bg();
  await openTheme();
  check('theme defaults to native', (await page.inputValue('#theme')) === 'native');
  await page.selectOption('#theme', 'dark');
  check('theme previewed', (await bg()) !== lightBg, await bg());
  await page.click('dialog[open] button:has-text("Cancel")');
  // The dialog loses [open] before its close event runs the revert.
  await waitFor(() => !document.documentElement.dataset.theme);
  check('cancel reverts the theme', (await bg()) === lightBg);
  await saveTheme('dark');
  check('theme saved', (await page.evaluate(() => window.__theme)) === 'dark');
  check('dark theme matches the system dark palette', systemDark.includes('#181a1f') && (await palette()) === systemDark, systemDark);
  await page.emulateMedia({ colorScheme: 'dark' });
  await saveTheme('light');
  check('light theme overrides a dark system', (await bg()) === lightBg, await bg());
  await saveTheme('native');
  check('native theme follows the system', (await palette()) === systemDark);
  await page.emulateMedia({ colorScheme: 'light' });

  // An invalid language keeps the dialog open with an error.
  await page.click('#settings-button');
  await page.waitForSelector('dialog[open] #audio-languages');
  check('audio languages reload', (await page.inputValue('#audio-languages')) === 'sv,en');
  await page.fill('#audio-languages', 'english');
  await page.click('dialog[open] button:has-text("Save")');
  await page.waitForSelector('.toast.error');
  check('invalid audio language shown', (await page.textContent('.toast.error')).includes('language code'));
  check('invalid audio language rejected', (await page.evaluate(() => window.__audioLanguages)) === 'sv,en');
  await page.click('dialog[open] button:has-text("Cancel")');
  await page.waitForSelector('dialog[open]', { state: 'detached' });
  await page.click('.toast.error'); // dismiss, so later checks see their own toast

  // Logging: the level is saved, the folder opens from Go, and uncaught
  // frontend errors reach the log.
  await page.click('#settings-button');
  await page.waitForSelector('dialog[open] #log-level');
  check('log level defaults to info', (await page.inputValue('#log-level')) === 'info');
  await page.click('dialog[open] button:has-text("Open log folder")');
  await waitFor(() => window.__calls.some(([m]) => m === 'OpenLogFolder'));
  check('open log folder', true);
  await page.selectOption('#log-level', 'warn');
  await page.click('dialog[open] button:has-text("Save")');
  await page.waitForSelector('dialog[open]', { state: 'detached' });
  await page.click('#settings-button');
  await page.waitForSelector('dialog[open] #log-level');
  check('log level saved', (await page.inputValue('#log-level')) === 'warn');
  await page.keyboard.press('Escape');
  await page.waitForSelector('dialog[open]', { state: 'detached' });
  await page.evaluate(() =>
    setTimeout(() => {
      throw new Error('e2e boom');
    }),
  );
  await waitFor(() => window.__logs.some((l) => l.msg === 'uncaught error'));
  const logged = await page.evaluate(() => window.__logs.find((l) => l.msg === 'uncaught error'));
  check(
    'uncaught error logged',
    logged.level === 'error' && logged.attrs.message === 'Error: e2e boom' && 'stack' in logged.attrs,
    JSON.stringify(logged),
  );
  // The deliberate error is not a problem with the app.
  problems.splice(0, problems.length, ...problems.filter((p) => !p.includes('e2e boom')));

  // Updates: up to date, then a newer release confirmed and installed.
  await page.click('#settings-button');
  await page.click('dialog[open] button:has-text("Check for updates")');
  await page.waitForSelector('.toast.info');
  check('up to date', (await page.textContent('.toast.info')).includes('latest version (v0.0.0-test)'));
  await page.evaluate(() => (window.__latest = 'v9.9.9'));
  await page.click('dialog[open] button:has-text("Check for updates")');
  await waitFor(() => document.querySelectorAll('dialog[open]').length === 2);
  check('update offered', (await page.textContent('dialog[open] >> nth=1')).includes('v9.9.9 is available'));
  await shot('05b-update');
  await page.click('dialog[open] button:has-text("Install and restart")');
  await waitFor(() => window.__installed && document.querySelector('.version-row button')?.textContent === 'Restarting…');
  check('update installed', true);
  await page.keyboard.press('Escape');
  await page.waitForSelector('dialog[open]', { state: 'detached' });

  // macOS app menu: Go sends the chosen item as an event.
  await page.evaluate(() => window.__emit('menu-settings'));
  await page.waitForSelector('dialog[open] .version-row');
  await page.evaluate(() => window.__emit('menu-settings'));
  check('menu settings opens once', (await page.$$('dialog[open]')).length === 1);
  await page.keyboard.press('Escape');
  await page.waitForSelector('dialog[open]', { state: 'detached' });
  await page.evaluate(() => {
    window.__latest = 'v0.0.0-test';
    document.querySelectorAll('.toast').forEach((t) => t.remove());
  });
  const statusBefore = await page.textContent('#status');
  await page.evaluate(() => window.__emit('menu-check-update'));
  await page.waitForSelector('.toast.info');
  check('menu update check', (await page.textContent('.toast.info')).includes('latest version (v0.0.0-test)'));
  check('menu update check restores status', (await page.textContent('#status')) === statusBefore);

  // Add playlist: validation, server error, success with progress.
  await page.click('#add-playlist');
  await page.waitForSelector('dialog[open]');
  await shot('06-add');
  await page.click('dialog[open] button:has-text("Add URL")');
  check('empty URL keeps dialog open', (await page.$('dialog[open]')) !== null);
  await page.fill('dialog[open] input[type=url]', 'ftp://example.com/list.m3u');
  await page.click('dialog[open] button:has-text("Add URL")');
  await page.waitForSelector('.toast.error');
  check('backend error shown', (await page.textContent('.toast.error')).includes('only http'));
  await page.click('.toast.error');

  await page.click('#add-playlist');
  await page.fill('dialog[open] input[type=text]', 'My Provider');
  await page.fill('dialog[open] input[type=url]', 'https://provider.example/get.php');
  await page.click('dialog[open] button:has-text("Add URL")');
  await waitFor(() => /^Importing playlist… .*channels/.test(document.querySelector('#status')?.textContent));
  check('import progress shown', !(await page.isHidden('#cancel-import')));
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Added'));
  check('new playlist selected', (await page.textContent('#playlists .selected .item-name')) === 'My Provider');
  check('import summary', (await page.textContent('#status')).includes('3 skipped'), await page.textContent('#status'));

  // Edit: change a URL playlist's URL, e.g. new credentials.
  await page.click('#playlists .list-item.selected button[title="Edit"]');
  check('URL playlist shows its redacted URL', (await page.textContent('dialog[open]')).includes('Current: http://provider.example/…'));
  await page.fill('dialog[open] input[name=source]', 'not a url');
  await page.click('dialog[open] button:has-text("Save")');
  check('invalid playlist URL keeps the dialog open', (await page.$('dialog[open]')) !== null);
  await page.fill('dialog[open] input[name=source]', 'https://new.example/get.php?username=u&password=secret');
  await page.click('dialog[open] button:has-text("Save")');
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Changed the URL of “My Provider”'));
  const urlCalls = await page.evaluate(() => window.__calls.filter(([m]) => m === 'SetPlaylistURL'));
  check('playlist URL changed', JSON.stringify(urlCalls) === JSON.stringify([['SetPlaylistURL', 3, 'https://new.example/get.php?username=u&password=secret']]), JSON.stringify(urlCalls));
  check('the playlist URL is never shown', !(await page.content()).includes('secret'));

  // Edit: rename and add a program guide; then delete.
  await page.click('#playlists .list-item:has-text("Small List")');
  await waitFor(() => document.querySelector('#channels-count')?.textContent === '3 channels');
  await waitFor(() => document.querySelector('#channel-details')?.textContent?.includes('No program guide'));
  check('details explain a missing guide', true);
  await page.click('#playlists .list-item.selected button[title="Edit"]');
  await page.fill('dialog[open] input[type=text]', 'Renamed List');
  await page.fill('dialog[open] input[type=url]', 'file:///etc/passwd');
  await page.click('dialog[open] button:has-text("Save")');
  check('invalid guide URL keeps the dialog open', (await page.$('dialog[open]')) !== null);
  await page.fill('dialog[open] input[type=url]', 'https://epg.example/guide.xml.gz?user=u&pass=p');
  await shot('08-edit-playlist');
  await page.click('dialog[open] button:has-text("Save")');
  await page.waitForSelector('#playlists .item-name:has-text("Renamed List")');
  check('rename', true);
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Updated the program guide'));
  const guideCalls = await page.evaluate(() => window.__calls.filter(([m]) => m === 'SetPlaylistGuide' || m === 'RefreshGuide'));
  check('guide URL saved and loaded', JSON.stringify(guideCalls.slice(-2)) === JSON.stringify([['SetPlaylistGuide', 2, 'https://epg.example/guide.xml.gz?user=u&pass=p'], ['RefreshGuide', 2]]), JSON.stringify(guideCalls));
  await waitFor(() => document.querySelector('.channel-row .channel-now'));
  await waitFor(() => document.querySelector('#channel-details .programme-title'));
  check('guide shown after loading it', true);
  check('the guide URL is never shown', !(await page.content()).includes('pass=p'));
  await page.click('#playlists .list-item.selected button[title="Delete"]');
  await page.click('dialog[open] button:has-text("Delete")');
  await waitFor(() => document.querySelectorAll('#playlists li').length === 2);
  check('delete selects another playlist', (await page.$$('#playlists .selected')).length === 1);

  // Persisted selection survives a reload.
  const before = await page.textContent('#playlists .selected .item-name');
  await page.reload();
  await page.waitForSelector('#playlists .selected');
  check('selected playlist remembered', (await page.textContent('#playlists .selected .item-name')) === before);

  // Dark mode renders.
  await page.emulateMedia({ colorScheme: 'dark' });
  await shot('07-dark');

  check('no console errors, warnings or CSP violations', problems.length === 0, problems.join(' || '));
} catch (error) {
  check(`unexpected error: ${error.message.split('\n')[0]}`, false);
  await shot('failure');
} finally {
  await browser.close();
  server.close();
}

const failed = results.filter((ok) => !ok).length;
console.log(`\n${results.length - failed}/${results.length} passed`);
process.exit(failed ? 1 : 0);
