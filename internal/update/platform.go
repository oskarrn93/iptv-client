package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// errManual is wrapped by a Platform.Install error that handed the
// downloaded file to the user to install by hand, so it must be kept.
var errManual = errors.New("the update could not be installed automatically")

// Platform is the platform-specific part of an update (a strategy): which
// release asset to download and how to install it. All implementations
// compile on every OS so they can be tested anywhere; only Install has to
// run on its own OS.
type Platform interface {
	// AssetName is the release asset to download.
	AssetName() string
	// Install installs the downloaded asset and schedules a restart once the
	// app has exited. The caller must quit the app right after it returns nil.
	Install(assetPath string) error
}

// ForOS returns the update strategy for goos.
func ForOS(goos string) Platform {
	switch goos {
	case "windows":
		return windowsPlatform{}
	case "darwin":
		return darwinPlatform{}
	}
	return unsupported{goos}
}

// Current returns the update strategy for the running OS.
func Current() Platform { return ForOS(runtime.GOOS) }

// executable returns the running executable's path with symlinks resolved.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// unsupported is the strategy for platforms without a release asset.
type unsupported struct{ goos string }

func (unsupported) AssetName() string { return "" }

func (u unsupported) Install(string) error {
	return fmt.Errorf("updates are not supported on %s", u.goos)
}
