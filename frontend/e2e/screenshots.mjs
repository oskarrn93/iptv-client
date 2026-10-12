// Regenerates the README screenshots (docs/screenshot*.png) from the built
// frontend with fictional demo data (demo-data.js), in light and dark mode.
//
//   npm run screenshots              # uses Microsoft Edge (Windows default)
//   E2E_BROWSER=chrome npm run screenshots
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright-core';
import { serve } from './serve.mjs';

const docs = fileURLToPath(new URL('../../docs/', import.meta.url));
const server = await serve(['demo-data.js', 'stub-backend.js']);
const browser = await chromium.launch({ channel: process.env.E2E_BROWSER ?? 'msedge', headless: true });
try {
  for (const [scheme, file] of [['light', 'screenshot.png'], ['dark', 'screenshot-dark.png']]) {
    const page = await browser.newPage({ viewport: { width: 1200, height: 720 }, deviceScaleFactor: 2, colorScheme: scheme });
    await page.goto(server.url);
    await page.waitForSelector('#channels-count:has-text("channel")');
    await page.click('#groups :text-is("Sports")');
    await page.waitForSelector('#channels-title:text-is("Sports")');
    await page.screenshot({ path: join(docs, file) });
    console.log(`wrote docs/${file}`);
    await page.close();
  }
} finally {
  await browser.close();
  server.close();
}
