package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestLogger(t *testing.T) (*slog.Logger, *bytes.Buffer, *slog.LevelVar) {
	t.Helper()
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	return slog.New(newHandler(&buf, lv)).With("run", "abc123"), &buf, lv
}

type secretStruct struct{ URL string }

func TestRedactsEverything(t *testing.T) {
	l, buf, _ := newTestLogger(t)
	const secret = "S3cr3t"
	u := "http://user:" + secret + "@provider.tv/live/u/" + secret + "/1.ts"
	ctx := With(context.Background(), slog.String("from_ctx", u))
	l.With("from_with", u).InfoContext(ctx, "msg "+u,
		"str", u,
		"err", errors.New("Get "+u+": EOF"),
		"struct", secretStruct{u},
		slog.Group("g", "nested", u),
	)
	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("secret in log:\n%s", out)
	}
	for _, want := range []string{"run=abc123", "from_ctx=", "from_with=", "g.nested=", "http://provider.tv/…"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestContextAttrs(t *testing.T) {
	l, buf, _ := newTestLogger(t)
	ctx := With(context.Background(), slog.String("op", "refresh"))
	ctx2 := With(ctx, slog.Int64("playlist_id", 7))
	l.InfoContext(ctx2, "done")
	l.InfoContext(ctx, "other")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if !strings.Contains(lines[0], "op=refresh playlist_id=7") {
		t.Errorf("line 1: %s", lines[0])
	}
	if strings.Contains(lines[1], "playlist_id") {
		t.Errorf("parent context changed: %s", lines[1])
	}
}

func TestLevelAppliesToWails(t *testing.T) {
	l, buf, lv := newTestLogger(t)
	w := Wails(l)
	w.Debug("hidden")
	w.Trace("never")
	lv.Set(slog.LevelDebug)
	w.Debug("shown")
	w.Trace("never")
	w.Error(`process message error: C{"args":["http://u:p@h/x"]} -> boom` + "\n")
	out := buf.String()
	if strings.Contains(out, "hidden") || strings.Contains(out, "never") {
		t.Errorf("level not applied:\n%s", out)
	}
	if !strings.Contains(out, "msg=shown") || !strings.Contains(out, "component=wails") {
		t.Errorf("missing debug line:\n%s", out)
	}
	if strings.Contains(out, "u:p@") || strings.Contains(out, `\n"`) {
		t.Errorf("not redacted or newline kept:\n%s", out)
	}
}

func TestParseLevel(t *testing.T) {
	for _, s := range []string{LevelDebug, LevelInfo, LevelWarn, LevelError} {
		l, err := ParseLevel(s)
		if err != nil || LevelName(l) != s {
			t.Errorf("%s: %v %v", s, l, err)
		}
	}
	for _, s := range []string{"", "INFO", "trace", "warning"} {
		if _, err := ParseLevel(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func testRotator(t *testing.T, now *time.Time) *rotator {
	t.Helper()
	r := newRotator(t.TempDir(), Meta{Version: "v1.2.3", Run: "abc123"})
	r.now = func() time.Time { return *now }
	t.Cleanup(func() { r.Close() })
	return r
}

func logFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, filePrefix+"*.log"))
	for i := range m {
		m[i] = filepath.Base(m[i])
	}
	return m
}

func TestRotatesDailyAndBySize(t *testing.T) {
	now := time.Date(2026, 10, 11, 23, 59, 0, 0, time.Local)
	r := testRotator(t, &now)
	r.Write([]byte("a\n"))
	now = now.Add(2 * time.Minute)
	r.Write([]byte("b\n"))
	big := bytes.Repeat([]byte("x"), maxFileSize/2)
	r.Write(big)
	r.Write(big) // over the limit: new part
	got := strings.Join(logFiles(t, r.dir), " ")
	want := "iptv-client-2026-10-11-abc123.log iptv-client-2026-10-12-abc123-2.log iptv-client-2026-10-12-abc123.log"
	if got != want {
		t.Fatalf("files:\n got %s\nwant %s", got, want)
	}
	first, _ := os.ReadFile(filepath.Join(r.dir, "iptv-client-2026-10-11-abc123.log"))
	if !strings.Contains(string(first), `msg="log file opened" run=abc123 version=v1.2.3`) || !strings.HasSuffix(string(first), "a\n") {
		t.Errorf("header or content wrong:\n%s", first)
	}
}

func TestPrune(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.Local)
	r := testRotator(t, &now)
	write := func(name string, size int, age time.Duration) {
		p := filepath.Join(r.dir, name)
		os.WriteFile(p, nil, 0o600)
		os.Truncate(p, int64(size)) // sized without writing the data
		os.Chtimes(p, now.Add(-age), now.Add(-age))
	}
	write("iptv-client-2026-09-01-old.log", 10, 40*24*time.Hour) // too old
	write("crash-old.tmp", 0, 40*24*time.Hour)                   // too old
	write("crash-running.tmp", 0, time.Hour)                     // kept
	write("unrelated.txt", 10, 40*24*time.Hour)                  // not ours
	for i := range 10 {
		// 10 x 15 MB: only the newest fit in 100 MB with the current file.
		write("iptv-client-2026-10-0"+string(rune('0'+i))+"-x.log", 15<<20, time.Duration(10-i)*time.Hour)
	}
	if err := r.open(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(r.dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	got := strings.Join(names, " ")
	want := "crash-running.tmp iptv-client-2026-10-04-x.log iptv-client-2026-10-05-x.log iptv-client-2026-10-06-x.log " +
		"iptv-client-2026-10-07-x.log iptv-client-2026-10-08-x.log iptv-client-2026-10-09-x.log " +
		"iptv-client-2026-10-11-abc123.log unrelated.txt"
	if got != want {
		t.Errorf("after prune:\n got %s\nwant %s", got, want)
	}
}

func TestSetupAndRecoverCrashes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "crash-dead01.tmp"), []byte("panic: GET http://u:p@h/x\ngoroutine 1"), 0o600)
	os.WriteFile(filepath.Join(dir, "crash-alive1.tmp"), nil, 0o600)
	l, err := Setup(dir, Meta{Version: "v1", Run: "run001"})
	if err != nil {
		t.Fatal(err)
	}
	defer slog.SetDefault(slog.New(slog.DiscardHandler))
	RecoverCrashes(context.Background(), l.Logger, dir)
	l.Logger.Info("hello")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"crash-dead01.tmp": false, "crash-alive1.tmp": true, "crash-run001.tmp": false} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "iptv-client-*-run001.log"))
	if len(matches) != 1 {
		t.Fatalf("log files: %v", matches)
	}
	b, _ := os.ReadFile(matches[0])
	out := string(b)
	for _, want := range []string{`msg="previous run crashed"`, "crashed_run=dead01", "http://h/…", "msg=hello run=run001"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "u:p@") {
		t.Errorf("crash output not redacted:\n%s", out)
	}
}

func TestDirFollowsDatabaseOverride(t *testing.T) {
	db := filepath.Join(t.TempDir(), "scratch.db")
	t.Setenv("IPTV_CLIENT_DB", db)
	got, err := Dir()
	if err != nil || got != filepath.Join(filepath.Dir(db), "logs") {
		t.Errorf("Dir() = %q, %v", got, err)
	}
}

func TestInvalidLevelNotEchoed(t *testing.T) {
	_, err := ParseLevel(strings.Repeat("x", 1<<20))
	if err == nil || err.Error() != "invalid log level" {
		t.Errorf("err = %.80v", err)
	}
}

func TestWailsLimits(t *testing.T) {
	l, buf, _ := newTestLogger(t)
	w := Wails(l).(wailsAdapter)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w.limit.now = func() time.Time { return now }
	w.Error(strings.Repeat("y", 1<<20))
	if n := buf.Len(); n > wailsMaxMsg+500 {
		t.Errorf("line not capped: %d bytes", n)
	}
	for range rateBurst + 10 {
		w.Info("spam")
	}
	if n := strings.Count(buf.String(), "msg=spam"); n != rateBurst-1 {
		t.Errorf("logged %d lines in a burst, want %d", n, rateBurst-1)
	}
	now = now.Add(time.Second)
	w.Info("after")
	if !strings.Contains(buf.String(), `msg="wails log lines dropped" run=abc123 component=wails n=11`) {
		t.Errorf("missing drop summary:\n%s", buf.String()[strings.LastIndex(buf.String(), "spam"):])
	}
}
