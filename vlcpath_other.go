//go:build !windows

package main

import (
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func vlcFilters() []runtime.FileFilter { return nil }

// vlcExecutable maps a picked VLC.app bundle to the binary inside it.
func vlcExecutable(picked string) string {
	if strings.HasSuffix(strings.ToLower(picked), ".app") {
		return filepath.Join(picked, "Contents", "MacOS", "VLC")
	}
	return picked
}
