package logging

import (
	"context"
	"log/slog"
	"strings"

	wailslogger "github.com/wailsapp/wails/v2/pkg/logger"
)

// WailsLevel is the level to give Wails (options.App LogLevel and
// LogLevelProduction): Debug, so Wails never sends Trace messages. Those
// include every bound-method result (channel lists, settings) and would
// flood the log. The user's level is applied by the slog handler.
const WailsLevel = wailslogger.DEBUG

// wailsMaxMsg caps one Wails message. Scripts in the webview can make Wails
// log (runtime.LogInfo etc., or a call whose arguments fail to parse, which
// logs the raw call), so its messages are size- and rate-limited like UI
// lines.
const wailsMaxMsg = 4 << 10

// Wails returns an adapter that sends Wails' own messages to l with
// component=wails. Messages are redacted like everything else; that matters
// because Wails logs the raw call, arguments included, when a bound method
// panics or its arguments cannot be parsed.
func Wails(l *slog.Logger) wailslogger.Logger {
	return wailsAdapter{l.With("component", "wails"), newLimiter()}
}

type wailsAdapter struct {
	l     *slog.Logger
	limit *limiter
}

func (a wailsAdapter) log(level slog.Level, msg string) {
	ctx := context.Background()
	if a.limit.admit(ctx, a.l, level, "wails log lines dropped") {
		// Wails messages end in a newline.
		a.l.Log(ctx, level, truncate(strings.TrimSuffix(msg, "\n"), wailsMaxMsg))
	}
}

func (a wailsAdapter) Print(msg string)   { a.log(slog.LevelInfo, msg) }
func (a wailsAdapter) Trace(string)       {}
func (a wailsAdapter) Debug(msg string)   { a.log(slog.LevelDebug, msg) }
func (a wailsAdapter) Info(msg string)    { a.log(slog.LevelInfo, msg) }
func (a wailsAdapter) Warning(msg string) { a.log(slog.LevelWarn, msg) }
func (a wailsAdapter) Error(msg string)   { a.log(slog.LevelError, msg) }

// Fatal is logged as an error; Wails then exits the process. Note that
// scripts in the webview can trigger this too (runtime.LogFatal).
func (a wailsAdapter) Fatal(msg string) { a.log(slog.LevelError, msg) }
