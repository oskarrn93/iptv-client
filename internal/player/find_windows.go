package player

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func platformLocators() []locator {
	return []locator{
		registryKeys{`SOFTWARE\VideoLAN\VLC`, `SOFTWARE\WOW6432Node\VideoLAN\VLC`},
		programFiles{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"},
	}
}

// registryKeys reads the install location written by the VLC installer.
type registryKeys []string

func (keys registryKeys) candidates() []string {
	var out []string
	for _, key := range keys {
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
	return out
}

// programFiles checks the default install folder under each Program Files
// directory named by the given environment variables.
type programFiles []string

func (envs programFiles) candidates() []string {
	var out []string
	for _, env := range envs {
		if dir := os.Getenv(env); dir != "" {
			out = append(out, filepath.Join(dir, "VideoLAN", "VLC", "vlc.exe"))
		}
	}
	return out
}
