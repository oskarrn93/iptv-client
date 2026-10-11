package main

import "github.com/wailsapp/wails/v2/pkg/runtime"

func vlcFilters() []runtime.FileFilter {
	return []runtime.FileFilter{{DisplayName: "VLC (vlc.exe)", Pattern: "vlc.exe"}}
}

func vlcExecutable(picked string) string { return picked }
