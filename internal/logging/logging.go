// Package logging writes the app's structured logs (log/slog, text format)
// to daily rotated files in the OS's usual log folder.
//
// Every message and attribute value is passed through redact.String before
// it is written, as a backstop for the rule that code never logs stream or
// playlist URLs, stream options, the User-Agent or paths under the home
// directory. Log IDs (playlist_id, channel_id), not names.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"iptv-client/internal/database"
	"iptv-client/internal/redact"
)

// Meta describes the running process. It goes in each file's header.
type Meta struct {
	Version string
	Run     string // see NewRun
}

// NewRun returns a short random ID for this process. It is on every log
// line and in the file names, so lines can be traced to their run.
func NewRun() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Logs is what Setup returns. Only main uses it; everything else gets the
// standard library types it holds: the *slog.Logger and, to change the
// level at runtime, the *slog.LevelVar.
type Logs struct {
	Logger *slog.Logger
	Level  *slog.LevelVar // starts at Info
	Dir    string         // the folder the files are written to

	w     *rotator // nil when logging to stderr
	crash *os.File
}

// Setup creates dir if needed and returns a logger writing rotated files
// there, installed as slog's default (so the standard log package writes
// there too). If dir cannot be used, it logs to stderr and returns the error
// alongside working Logs.
func Setup(dir string, m Meta) (*Logs, error) {
	l := &Logs{Level: new(slog.LevelVar), Dir: dir}
	var out io.Writer = os.Stderr
	err := os.MkdirAll(dir, 0o700)
	if err == nil {
		l.w = newRotator(dir, m)
		if err = l.w.open(); err == nil {
			out = l.w
		} else {
			l.w = nil
		}
	}
	l.Logger = slog.New(newHandler(out, l.Level)).With("run", m.Run)
	slog.SetDefault(l.Logger)
	if err != nil {
		l.Logger.Error("cannot write log files; logging to stderr", "err", err)
		return l, err
	}
	l.crash = setCrashOutput(dir, m.Run)
	return l, nil
}

// Close stops logging to the current file. A clean exit removes this run's
// empty crash file.
func (l *Logs) Close() error {
	if l.crash != nil {
		removeCrashOutput(l.crash)
	}
	if l.w != nil {
		return l.w.Close()
	}
	return nil
}

// Dir returns the log folder: next to the database when $IPTV_CLIENT_DB
// is set (so a scratch database gets its own logs), otherwise
// ~/Library/Logs/IPTVClient on macOS and %LocalAppData%\IPTVClient\logs on
// Windows.
func Dir() (string, error) {
	if p := os.Getenv(database.PathEnv); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(abs), "logs"), nil
	}
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Logs", database.AppDirName), nil
	}
	cache, err := os.UserCacheDir() // %LocalAppData% on Windows
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, database.AppDirName, "logs"), nil
}

// --- levels ---

// Level names accepted by ParseLevel and returned by LevelName.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// ParseLevel parses one of the level names.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case LevelDebug:
		return slog.LevelDebug, nil
	case LevelInfo:
		return slog.LevelInfo, nil
	case LevelWarn:
		return slog.LevelWarn, nil
	case LevelError:
		return slog.LevelError, nil
	}
	// Not echoed: the value can come from the UI and be of any size.
	return 0, errors.New("invalid log level")
}

// LevelName returns the name of level, rounded down to a known level.
func LevelName(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return LevelError
	case level >= slog.LevelWarn:
		return LevelWarn
	case level >= slog.LevelInfo:
		return LevelInfo
	}
	return LevelDebug
}

// --- context attributes ---

type ctxKey struct{}

// With returns ctx carrying attrs, which are added to every line logged
// with that context (InfoContext etc.).
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	return context.WithValue(ctx, ctxKey{}, append(prev[:len(prev):len(prev)], attrs...))
}

// handler adds context attributes and redacts every value.
type handler struct{ slog.Handler }

func newHandler(w io.Writer, level slog.Leveler) slog.Handler {
	return handler{slog.NewTextHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr})}
}

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(ctxKey{}).([]slog.Attr); ok {
		r.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, r)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler{h.Handler.WithAttrs(attrs)}
}

func (h handler) WithGroup(name string) slog.Handler {
	return handler{h.Handler.WithGroup(name)}
}

// redactAttr runs on the message and every attribute, including those from
// With, Logger.With and the context.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(redact.String(a.Value.String()))
	case slog.KindAny:
		// Errors, Stringers, structs: format, then redact the text.
		var s string
		switch v := a.Value.Any().(type) {
		case error:
			s = v.Error()
		default:
			s = fmt.Sprint(v)
		}
		a.Value = slog.StringValue(redact.String(s))
	}
	return a
}

// trimNewline removes one trailing newline (Wails messages end in one).
func trimNewline(s string) string { return strings.TrimSuffix(s, "\n") }
