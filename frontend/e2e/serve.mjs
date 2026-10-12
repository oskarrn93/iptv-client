// Serves frontend/dist with e2e scripts injected ahead of the app, standing in
// for the scripts Wails injects into the page. Used by run.mjs and
// screenshots.mjs.
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = fileURLToPath(new URL('.', import.meta.url));
const dist = join(here, '..', 'dist');
const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' };

// scripts: file names in e2e/, loaded in order before the app.
export async function serve(scripts) {
  const server = createServer(async (req, res) => {
    try {
      const path = new URL(req.url, 'http://x').pathname;
      const script = scripts.find((s) => path === `/e2e/${s}`);
      if (script) {
        res.writeHead(200, { 'content-type': types['.js'] }).end(await readFile(join(here, script)));
        return;
      }
      const file = normalize(join(dist, path === '/' ? 'index.html' : path));
      if (!file.startsWith(dist)) throw new Error('outside dist');
      let body = await readFile(file);
      if (file.endsWith('index.html')) {
        const tags = scripts.map((s) => `<script src="/e2e/${s}"></script>\n  `).join('');
        body = body.toString().replace('<meta charset', `${tags}<meta charset`);
      }
      res.writeHead(200, { 'content-type': types[extname(file)] ?? 'application/octet-stream' }).end(body);
    } catch {
      res.writeHead(404).end();
    }
  });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  return { url: `http://127.0.0.1:${server.address().port}/`, close: () => server.close() };
}
