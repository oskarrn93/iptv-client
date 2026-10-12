package logging

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestUILog(t *testing.T) {
	l, buf, lv := newTestLogger(t)
	u := NewUI(l)
	u.Log("trace", "bad level", nil)
	u.Log("debug", "below level", nil)
	u.Log("error", "uncaught error", map[string]string{
		"message":   "x is undefined",
		"stack":     "at f (http://wails.localhost/assets/index.js:1:2)\nat g (http://u:p@evil.tv/x.js:1:1)",
		"component": "go", // reserved
		"run":       "forged",
		"Bad-Key":   "dropped",
		"long":      strings.Repeat("é", uiMaxValue),
	})
	out := buf.String()
	if strings.Contains(out, "bad level") || strings.Contains(out, "below level") {
		t.Errorf("level not applied:\n%s", out)
	}
	for _, bad := range []string{"component=go", "forged", "Bad-Key", "u:p@"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q in:\n%s", bad, out)
		}
	}
	for _, want := range []string{"level=ERROR", `msg="uncaught error"`, "component=ui", "run=abc123",
		"http://wails.localhost/assets/index.js:1:2", "http://evil.tv/…", "…"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if len(out) > uiMaxMsg+uiMaxStack+uiMaxValue+1000 {
		t.Errorf("line too long: %d bytes", len(out))
	}
	lv.Set(slog.LevelDebug)
	buf.Reset()
	u.Log("debug", "now shown", nil)
	if !strings.Contains(buf.String(), "now shown") {
		t.Errorf("debug not shown after level change")
	}
}

func TestUIRateLimit(t *testing.T) {
	l, buf, _ := newTestLogger(t)
	u := NewUI(l)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	u.limit.now = func() time.Time { return now }
	for range rateBurst + 50 {
		u.Log("info", "spam", nil)
	}
	if n := strings.Count(buf.String(), "msg=spam"); n != rateBurst {
		t.Errorf("logged %d lines in a burst, want %d", n, rateBurst)
	}
	now = now.Add(time.Second)
	u.Log("info", "after", nil)
	out := buf.String()
	if !strings.Contains(out, `msg="ui log lines dropped" run=abc123 component=ui n=50`) || !strings.Contains(out, "msg=after") {
		t.Errorf("missing drop summary or line after pause:\n%s", out[strings.LastIndex(out, "spam"):])
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("ab", 5); got != "ab" {
		t.Error(got)
	}
	if got := truncate("aéé", 2); got != "a…" {
		t.Errorf("split rune: %q", got)
	}
	if got := truncate("a\xffb", 10); got != "a�b" {
		t.Errorf("invalid UTF-8: %q", got)
	}
}
