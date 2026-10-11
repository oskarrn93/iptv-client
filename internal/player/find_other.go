//go:build !windows && !darwin

package player

// Other systems (Linux, used for CI) are not release targets; check the usual
// install paths before the PATH lookup.
func platformLocators() []locator {
	return []locator{fixedPaths{"/usr/bin/vlc", "/snap/bin/vlc"}}
}
