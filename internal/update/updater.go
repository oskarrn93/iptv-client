package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/mod/semver"
)

// ErrNoUpdate is returned by Updater.Install when Check found no newer
// release.
var ErrNoUpdate = errors.New("no update to install; check for updates first")

// ErrInstalling is returned by Updater.Install while another install runs or
// after one has started and the app is quitting.
var ErrInstalling = errors.New("the update is already being installed")

// Updater remembers the newer release found by Check so Install can install
// it, and allows one install at a time. It is safe for concurrent use.
type Updater struct {
	Checker   Checker
	Current   string // running version, e.g. "v1.2.3"
	UserAgent string

	mu         sync.Mutex
	release    *Release // newer release found by the last Check
	installing bool     // stays true once an install has started successfully
}

// Check looks up the latest release and reports whether it is newer than
// Current.
func (u *Updater) Check(ctx context.Context) (Release, bool, error) {
	if !semver.IsValid(u.Current) {
		return Release{}, false, ErrDevBuild
	}
	u.setRelease(nil)
	r, err := u.Checker.Latest(ctx, u.UserAgent)
	if err != nil {
		return Release{}, false, err
	}
	newer, err := Newer(u.Current, r.Version)
	if err != nil {
		return Release{}, false, err
	}
	if newer {
		u.setRelease(&r)
	}
	return r, newer, nil
}

func (u *Updater) setRelease(r *Release) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.release = r
}

// Install downloads the release found by Check and starts its installation.
// progress receives the download percentage. The caller must quit the app
// right after it returns nil; later calls return ErrInstalling.
func (u *Updater) Install(ctx context.Context, progress func(int)) (err error) {
	r, err := u.begin()
	if err != nil {
		return err
	}
	defer func() { u.end(err == nil) }()
	path, err := u.Checker.Download(ctx, r, u.UserAgent, progress)
	if err != nil {
		return err
	}
	if err = u.Checker.platform().Install(path); err != nil && !errors.Is(err, errManual) {
		// The installer was not started, so nothing else will remove it.
		// A manual install still needs the file the user was shown.
		os.RemoveAll(filepath.Dir(path))
	}
	return err
}

func (u *Updater) begin() (Release, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case u.installing:
		return Release{}, ErrInstalling
	case u.release == nil:
		return Release{}, ErrNoUpdate
	}
	u.installing = true
	return *u.release, nil
}

func (u *Updater) end(started bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.installing = started
}
