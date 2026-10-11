package player

import (
	"os/exec"
	"path/filepath"
)

// A locator is one strategy for finding VLC in a particular environment. It
// returns candidate paths in priority order; they need not exist.
type locator interface {
	candidates() []string
}

// fixedPaths is a locator for well-known absolute install paths.
type fixedPaths []string

func (p fixedPaths) candidates() []string { return p }

// pathLookup finds an executable by name on PATH.
type pathLookup string

func (name pathLookup) candidates() []string {
	if p, err := exec.LookPath(string(name)); err == nil {
		return []string{p}
	}
	return nil
}

// appBundles finds VLC.app in macOS Applications folders. Launching the
// binary inside the bundle (rather than `open -a VLC`) is what lets us pass
// arguments such as --meta-title. It starts a new VLC instance per stream;
// `open` would reuse a running VLC but silently drop arguments.
type appBundles []string

func (dirs appBundles) candidates() []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, filepath.Join(d, "VLC.app", "Contents", "MacOS", "VLC"))
	}
	return out
}

// locators returns the strategies for the current OS, ending with a PATH
// lookup as the last resort.
func locators() []locator {
	return append(platformLocators(), pathLookup("vlc"))
}

// locate returns the first candidate from ls that is an existing file.
func locate(ls []locator) (string, bool) {
	for _, l := range ls {
		for _, p := range l.candidates() {
			if isFile(p) {
				return p, true
			}
		}
	}
	return "", false
}
