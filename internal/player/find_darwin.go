package player

import (
	"os"
	"path/filepath"
)

func platformLocators() []locator {
	dirs := appBundles{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return []locator{dirs}
}
