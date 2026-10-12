// Frontend logging. Lines go to the app's log file through Go (App.Log),
// tagged component=ui, where they are validated, size-limited, redacted and
// rate-limited, and dropped below the log level. Like Go code, log IDs,
// never channel or playlist names or URLs.

import { Log } from '../wailsjs/go/main/App';

export type Level = 'debug' | 'info' | 'warn' | 'error';

const send = (level: Level, msg: string, attrs: Record<string, unknown> = {}): void => {
  const values: Record<string, string> = {};
  for (const [k, v] of Object.entries(attrs)) {
    if (v !== undefined && v !== null) values[k] = String(v);
  }
  // Fire and forget: a logging failure must never surface or log itself.
  try {
    Log(level, msg, values).catch(() => {});
  } catch {
    // The Go bridge is not available.
  }
};

export const log = {
  debug: (msg: string, attrs?: Record<string, unknown>) => send('debug', msg, attrs),
  info: (msg: string, attrs?: Record<string, unknown>) => send('info', msg, attrs),
  warn: (msg: string, attrs?: Record<string, unknown>) => send('warn', msg, attrs),
  error: (msg: string, attrs?: Record<string, unknown>) => send('error', msg, attrs),
};

const describe = (reason: unknown): { message: string; stack?: string } =>
  reason instanceof Error
    ? { message: `${reason.name}: ${reason.message}`, stack: reason.stack }
    : { message: typeof reason === 'string' ? reason : 'non-Error value' };

/** Logs uncaught errors and unhandled promise rejections. */
export const installErrorHandlers = (): void => {
  window.addEventListener('error', (e) => {
    log.error('uncaught error', {
      ...describe(e.error ?? e.message),
      file: e.filename,
      line: e.lineno,
      col: e.colno,
    });
  });
  window.addEventListener('unhandledrejection', (e) => {
    log.error('unhandled rejection', describe(e.reason));
  });
};
