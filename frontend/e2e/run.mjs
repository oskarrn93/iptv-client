// UI tests for the built frontend (frontend/dist, including its production
// CSP) against an in-memory stub of the Go API (stub-backend.js).
//
//   npm run test:e2e                 # uses Microsoft Edge (Windows default)
//   E2E_BROWSER=chrome npm run test:e2e
//   E2E_SCREENSHOTS=dir npm run test:e2e
//
// Uses an installed Edge/Chrome via playwright-core; no browser download.
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright-core';

const here = fileURLToPath(new URL('.', import.meta.url));
const dist = join(here, '..', 'dist');
const shots = process.env.E2E_SCREENSHOTS;
const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' };

const server = createServer(async (req, res) => {
  try {
    const path = new URL(req.url, 'http://x').pathname;
    if (path === '/e2e-stub.js') {
      res.writeHead(200, { 'content-type': types['.js'] }).end(await readFile(join(here, 'stub-backend.js')));
      return;
    }
    const file = normalize(join(dist, path === '/' ? 'index.html' : path));
    if (!file.startsWith(dist)) throw new Error('outside dist');
    let body = await readFile(file);
    if (file.endsWith('index.html')) {
      // The stub stands in for the scripts Wails injects into the page.
      body = body.toString().replace('<meta charset', '<script src="/e2e-stub.js"></script>\n  <meta charset');
    }
    res.writeHead(200, { 'content-type': types[extname(file)] ?? 'application/octet-stream' }).end(body);
  } catch {
    res.writeHead(404).end();
  }
});
await new Promise((r) => server.listen(0, '127.0.0.1', r));
const url = `http://127.0.0.1:${server.address().port}/`;

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
  check('no script ran from playlist data', await page.evaluate(() => window.__xss === undefined && !document.querySelector('#channels img')));
  const bold = await page.$$eval('#groups .item-name', (els) => els.map((e) => e.textContent));
  check('HTML in group name shown as text', bold.includes('<b>Bold Group</b>'));

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
  check('keyboard: arrows select, F favorites, Enter plays', played === 1 && (await page.textContent('#status')).includes(selected), selected);
  check('arrow key moved past the first result', !selected.startsWith('Kanal 4999 '), selected);

  // Double-click plays too.
  await page.dblclick('.channel-row:nth-child(1) .channel-name');
  await waitFor(() => window.__calls.filter(([m]) => m === 'Play').length === 2);
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
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Refreshed'));
  const forced = await page.evaluate(() => window.__calls.filter(([m]) => m === 'RefreshPlaylist').map(([, , f]) => f));
  check('refresh anyway forces', forced.join() === 'false,true', forced.join());

  // Settings.
  await page.click('#settings-button');
  await page.waitForSelector('dialog[open] .vlc-status.ok');
  check('settings shows VLC', (await page.textContent('dialog[open] .vlc-status')).includes('vlc.exe'));
  check('settings shows version', (await page.textContent('dialog[open]')).includes('IPTV Client v0.0.0-test'));
  await page.fill('dialog[open] input', 'MyPlayer/1.0');
  await shot('05-settings');
  await page.click('dialog[open] button:has-text("Save")');
  await page.waitForSelector('dialog[open]', { state: 'detached' });
  check('settings save', (await page.evaluate(() => window.__userAgent)) === 'MyPlayer/1.0');

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
  await waitFor(() => document.querySelector('#status')?.textContent?.includes('channels'));
  check('import progress shown', !(await page.isHidden('#cancel-import')));
  await waitFor(() => document.querySelector('#status')?.textContent?.startsWith('Added'));
  check('new playlist selected', (await page.textContent('#playlists .selected .item-name')) === 'My Provider');
  check('import summary', (await page.textContent('#status')).includes('3 skipped'), await page.textContent('#status'));

  // Rename and delete.
  await page.click('#playlists .list-item:has-text("Small List")');
  await waitFor(() => document.querySelector('#channels-count')?.textContent === '3 channels');
  await page.click('#playlists .list-item.selected button[title="Rename"]');
  await page.fill('dialog[open] input', 'Renamed List');
  await page.click('dialog[open] button:has-text("Save")');
  await page.waitForSelector('#playlists .item-name:has-text("Renamed List")');
  check('rename', true);
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
