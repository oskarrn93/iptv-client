//go:build !windows && !darwin

package player

// Other systems (Linux, used for CI) are not release targets; check the usual
// install paths and fall back to PATH lookup in Find.
func candidates() []string {
	return []string{"/usr/bin/vlc", "/snap/bin/vlc"}
}
