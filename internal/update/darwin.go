package update

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// notWritable reports whether err means the bundle's folder cannot be
// written: no permission, or a read-only volume (the disk image itself, or a
// translocated path).
func notWritable(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// darwinPlatform replaces the app bundle with the one in the DMG.
type darwinPlatform struct{}

func (darwinPlatform) AssetName() string { return "iptv-client-macos-universal.dmg" }

// Install replaces the running app bundle with the one in the downloaded
// disk image and schedules a relaunch once the app has exited.
//
// If the bundle's folder is not writable (e.g. the app was started from the
// disk image or a translocated path), the disk image is opened in Finder
// instead and an error asks the user to drag the app across.
func (darwinPlatform) Install(dmgPath string) error {
	bundle, err := runningBundle()
	if err != nil {
		return err
	}
	staged := filepath.Join(filepath.Dir(bundle), "."+filepath.Base(bundle)+".update")
	old := staged + ".old"
	os.RemoveAll(staged)
	os.RemoveAll(old)
	err = stage(dmgPath, staged)
	if err == nil {
		err = os.Rename(bundle, old)
	}
	if err != nil {
		os.RemoveAll(staged)
		if notWritable(err) {
			return manual(dmgPath)
		}
		return fmt.Errorf("replacing the app: %w", err)
	}
	if err := os.Rename(staged, bundle); err != nil {
		os.Rename(old, bundle)
		return fmt.Errorf("replacing the app: %w", err)
	}
	// The running executable stays valid after its bundle is deleted.
	os.RemoveAll(old)
	os.RemoveAll(filepath.Dir(dmgPath))

	// "open" on a bundle that is still running just activates it, so wait
	// for this process to exit first.
	cmd := exec.Command("/bin/sh", "-c",
		`while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; open "$2"`,
		"sh", strconv.Itoa(os.Getpid()), bundle)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("the update is installed but the app could not be restarted: %w", err)
	}
	return cmd.Process.Release()
}

// runningBundle returns the .app containing this executable.
func runningBundle() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	// <bundle>.app/Contents/MacOS/<exe>
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(bundle, ".app") {
		return "", errors.New("the app is not running from an application bundle")
	}
	return bundle, nil
}

// stage copies the .app from the disk image to dst.
func stage(dmgPath, dst string) error {
	// Fail early with a permission error, before mounting anything.
	if err := os.Mkdir(dst, 0o755); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil {
		return err
	}

	mnt, err := os.MkdirTemp("", "iptv-client-mount-")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen",
		"-mountpoint", mnt, dmgPath).CombinedOutput(); err != nil {
		return fmt.Errorf("opening the disk image: %s", strings.TrimSpace(string(out)))
	}
	defer exec.Command("hdiutil", "detach", "-force", mnt).Run()

	apps, _ := filepath.Glob(filepath.Join(mnt, "*.app"))
	if len(apps) != 1 {
		return errors.New("the disk image does not contain the app")
	}
	if out, err := exec.Command("ditto", apps[0], dst).CombinedOutput(); err != nil {
		return fmt.Errorf("copying the new version: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// manual opens the disk image for the user to install by hand.
func manual(dmgPath string) error {
	if err := exec.Command("open", dmgPath).Run(); err != nil {
		return fmt.Errorf("could not open the downloaded update: %w", err)
	}
	return fmt.Errorf("%w: the app's folder is not writable. "+
		"The new version has been opened: quit the app and drag it onto Applications to replace the old one", errManual)
}
