package player

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func candidates() []string {
	var out []string
	for _, key := range []string{`SOFTWARE\VideoLAN\VLC`, `SOFTWARE\WOW6432Node\VideoLAN\VLC`} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		// The default value is the full path to vlc.exe; InstallDir is its folder.
		if v, _, err := k.GetStringValue(""); err == nil && v != "" {
			out = append(out, v)
		}
		if v, _, err := k.GetStringValue("InstallDir"); err == nil && v != "" {
			out = append(out, filepath.Join(v, "vlc.exe"))
		}
		k.Close()
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if dir := os.Getenv(env); dir != "" {
			out = append(out, filepath.Join(dir, "VideoLAN", "VLC", "vlc.exe"))
		}
	}
	return out
}
