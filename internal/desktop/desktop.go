// Package desktop opens folders in the OS file manager.
package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenFolder shows dir in Explorer (Windows) or Finder (macOS). The file
// manager is started directly by its full path, never through a shell or a
// PATH lookup. dir must come from Go, not from the UI.
func OpenFolder(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("not a folder")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		cmd = exec.Command(filepath.Join(windir, "explorer.exe"), dir)
	case "darwin":
		cmd = exec.Command("/usr/bin/open", dir)
	default:
		return errors.New("opening folders is not supported on " + runtime.GOOS)
	}
	// explorer.exe exits with status 1 even when it succeeds, so only a
	// failure to start counts. Wait in the background to reap the process.
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
