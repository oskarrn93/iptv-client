package player

import (
	"os"
	"path/filepath"
)

// Launching the binary inside the bundle (rather than `open -a VLC`) is what
// lets us pass arguments such as --meta-title. It starts a new VLC instance
// per stream; `open` would reuse a running VLC but silently drop arguments.
func candidates() []string {
	out := []string{"/Applications/VLC.app/Contents/MacOS/VLC"}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, "Applications", "VLC.app", "Contents", "MacOS", "VLC"))
	}
	return out
}
