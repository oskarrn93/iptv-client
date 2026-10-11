package logging

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
)

// Rotation and retention limits.
const (
	maxFileSize  = 10 << 20  // start a new part beyond this
	maxTotalSize = 100 << 20 // all log and crash files together
	maxAge       = 14 * 24 * time.Hour
)

// filePrefix starts every log file name. Files are named
// iptv-client-<date>-<run>[-<part>].log, so processes never share a file.
const filePrefix = "iptv-client-"

// rotator is an io.Writer that writes to a file per day and run, starts a
// new part when a file grows too large, and prunes old files. It is safe
// for concurrent use.
type rotator struct {
	dir    string
	meta   Meta
	now    func() time.Time
	header func(time.Time) []byte

	mu   sync.Mutex
	f    *os.File
	day  string
	part int
	size int64
}

func newRotator(dir string, m Meta) *rotator {
	r := &rotator{dir: dir, meta: m, now: time.Now}
	r.header = r.defaultHeader
	return r
}

// open opens the first file, so Setup can report a failure.
func (r *rotator) open() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rotate(r.now())
}

func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.f == nil || now.Format(time.DateOnly) != r.day || (r.size > 0 && r.size+int64(len(p)) > maxFileSize) {
		if err := r.rotate(now); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// rotate closes the current file and opens the next one. Called with mu held.
func (r *rotator) rotate(now time.Time) error {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	day := now.Format(time.DateOnly)
	if day != r.day {
		r.day, r.part = day, 0
	}
	r.part++
	name := fmt.Sprintf("%s%s-%s.log", filePrefix, r.day, r.meta.Run)
	if r.part > 1 {
		name = fmt.Sprintf("%s%s-%s-%d.log", filePrefix, r.day, r.meta.Run, r.part)
	}
	f, err := os.OpenFile(filepath.Join(r.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	r.f, r.size = f, 0
	if fi, err := f.Stat(); err == nil {
		r.size = fi.Size()
	}
	n, _ := f.Write(r.header(now))
	r.size += int64(n)
	prune(r.dir, f.Name(), now)
	return nil
}

// defaultHeader is the first line of each file: facts about the build and
// system that do not change while the process runs. It is formatted with
// its own handler, not the logger, so it cannot deadlock on mu.
func (r *rotator) defaultHeader(now time.Time) []byte {
	var buf bytes.Buffer
	rec := slog.NewRecord(now, slog.LevelInfo, "log file opened", 0)
	rec.AddAttrs(
		slog.String("run", r.meta.Run),
		slog.String("version", r.meta.Version),
		slog.String("os", runtime.GOOS),
		slog.String("arch", runtime.GOARCH),
		slog.String("go", runtime.Version()),
		slog.String("wails", wailsVersion()),
	)
	_ = slog.NewTextHandler(&buf, nil).Handle(context.Background(), rec)
	return buf.Bytes()
}

func wailsVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/wailsapp/wails/v2" {
				return d.Version
			}
		}
	}
	return "unknown"
}

// prune deletes log and crash files older than maxAge, then the oldest ones
// until all of them fit in maxTotalSize. current is never deleted. Errors
// are ignored: another instance may hold a file open (Windows refuses to
// delete it) and pruning is retried at the next rotation anyway.
func prune(dir, current string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		mod  time.Time
		size int64
	}
	var files []file
	for _, e := range entries {
		name := e.Name()
		ours := (strings.HasPrefix(name, filePrefix) && strings.HasSuffix(name, ".log")) ||
			(strings.HasPrefix(name, crashPrefix) && strings.HasSuffix(name, crashSuffix))
		if !ours || e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, name)
		if path == current {
			continue
		}
		if now.Sub(fi.ModTime()) > maxAge {
			os.Remove(path)
			continue
		}
		// Crash files are empty while their process runs; deleting one would
		// lose that process's crash output, so they only expire by age.
		if strings.HasPrefix(name, crashPrefix) {
			continue
		}
		files = append(files, file{path, fi.ModTime(), fi.Size()})
	}
	// Newest first; keep while the total fits, counting the current file
	// as a full one.
	slices.SortFunc(files, func(a, b file) int { return b.mod.Compare(a.mod) })
	total := int64(maxFileSize)
	for _, f := range files {
		total += f.size
		if total > maxTotalSize {
			os.Remove(f.path)
		}
	}
}
