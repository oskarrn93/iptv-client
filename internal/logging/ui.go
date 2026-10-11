package logging

import (
	"context"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Limits on what the frontend may log. The UI renders untrusted playlist
// text, so its log calls are treated as untrusted input too.
const (
	uiMaxMsg      = 2 << 10
	uiMaxValue    = 2 << 10
	uiMaxStack    = 8 << 10
	uiMaxAttrs    = 16
	uiComponent   = "ui"
	uiDroppedLine = "ui log lines dropped"
)

// Rate limit for frontend lines and for Wails' messages (which scripts in
// the webview can also produce): a burst, then a steady rate.
const (
	ratePerSec = 20
	rateBurst  = 100
)

var uiKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// uiReserved are keys the logger sets itself; the UI cannot override them.
var uiReserved = map[string]bool{
	"time": true, "level": true, "msg": true, "source": true,
	"run": true, "component": true, "op": true,
}

// UI writes frontend log lines to the log with component=ui. It validates
// and size-limits every call and rate-limits them, so a render loop cannot
// flood the disk. It is safe for concurrent use.
type UI struct {
	l     *slog.Logger
	limit *limiter
}

// NewUI returns a frontend logger writing to l.
func NewUI(l *slog.Logger) *UI {
	return &UI{l: l.With("component", uiComponent), limit: newLimiter()}
}

// Log writes one frontend line. Invalid levels are ignored, invalid or
// reserved keys are dropped, and long values are cut.
func (u *UI) Log(level, msg string, attrs map[string]string) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return
	}
	ctx := context.Background()
	if !u.l.Enabled(ctx, lvl) {
		return
	}
	dropped, ok := u.limit.take()
	if dropped > 0 {
		u.l.Warn(uiDroppedLine, "n", dropped)
	}
	if !ok {
		return
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		if uiKey.MatchString(k) && !uiReserved[k] {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	if len(keys) > uiMaxAttrs {
		keys = keys[:uiMaxAttrs]
	}
	args := make([]any, 0, 2*len(keys))
	for _, k := range keys {
		limit := uiMaxValue
		if k == "stack" {
			limit = uiMaxStack
		}
		args = append(args, k, truncate(attrs[k], limit))
	}
	u.l.Log(ctx, lvl, truncate(msg, uiMaxMsg), args...)
}

// limiter is a token bucket that counts what it drops. It is safe for
// concurrent use.
type limiter struct {
	now func() time.Time

	mu      sync.Mutex
	tokens  float64
	last    time.Time
	dropped int
}

func newLimiter() *limiter { return &limiter{now: time.Now, tokens: rateBurst} }

// take consumes a token. It returns false if the line must be dropped, and
// the number of lines dropped before this one once lines flow again.
func (r *limiter) take() (dropped int, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if !r.last.IsZero() {
		r.tokens = min(rateBurst, r.tokens+now.Sub(r.last).Seconds()*ratePerSec)
	}
	r.last = now
	if r.tokens < 1 {
		r.dropped++
		return 0, false
	}
	r.tokens--
	dropped, r.dropped = r.dropped, 0
	return dropped, true
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
