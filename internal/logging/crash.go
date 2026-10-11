package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// A fatal error (an unrecovered panic in a goroutine, a runtime fault) is
// printed by the Go runtime to stderr, which a GUI build does not have. The
// runtime writes it to crash-<run>.tmp instead. The runtime cannot redact,
// so the file is only read back, redacted, into the log at the next start
// (RecoverCrashes) and then deleted.
const (
	crashPrefix = "crash-"
	crashSuffix = ".tmp"
	// maxCrashBytes is how much of a crash file's end is logged.
	maxCrashBytes = 64 << 10
)

func crashPath(dir, run string) string {
	return filepath.Join(dir, crashPrefix+run+crashSuffix)
}

// setCrashOutput directs fatal error output to this run's crash file.
// It returns nil if that is not possible.
func setCrashOutput(dir, run string) *os.File {
	f, err := os.OpenFile(crashPath(dir, run), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil
	}
	return f
}

// removeCrashOutput restores the default crash output and deletes the
// (empty) crash file of a run that exits normally.
func removeCrashOutput(f *os.File) {
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	f.Close()
	os.Remove(f.Name())
}

// RecoverCrashes logs to l the output of runs that crashed, redacted like
// any other log value, and deletes their crash files in dir. Run it at
// startup and before showing the folder to the user, so a raw crash file is
// never what they see. Empty crash files are left alone: they belong to runs
// that are still going (this one included) or were killed, and expire with
// the other log files.
func RecoverCrashes(ctx context.Context, l *slog.Logger, dir string) {
	if dir == "" {
		return
	}
	paths, _ := filepath.Glob(filepath.Join(dir, crashPrefix+"*"+crashSuffix))
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			continue
		}
		out, err := readTail(p, maxCrashBytes)
		if err != nil {
			continue
		}
		run := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), crashPrefix), crashSuffix)
		l.ErrorContext(ctx, "previous run crashed", "crashed_run", run, "output", out)
		os.Remove(p)
	}
}

func readTail(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > n {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	return string(b), err
}
