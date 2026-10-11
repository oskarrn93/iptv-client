import { defineConfig, type Plugin } from 'vite';

// Content Security Policy for production builds. Playlist data is untrusted,
// so even if markup ever slipped into the DOM it must not be able to run
// script or load remote resources (C3/C4 in the design review). It is only
// added to builds because the Vite dev server needs inline scripts and a
// websocket for hot reload.
const csp = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
].join('; ');

const cspPlugin = (): Plugin => ({
  name: 'inject-csp',
  apply: 'build',
  transformIndexHtml: () => [
    { tag: 'meta', attrs: { 'http-equiv': 'Content-Security-Policy', content: csp }, injectTo: 'head-prepend' },
  ],
});

export default defineConfig({
  plugins: [cspPlugin()],
  build: { target: 'es2022' },
});
