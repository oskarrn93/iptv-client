package player

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// A Player is the strategy for building one media player's command line.
// Players are the only programs Launch will start, so adding one widens
// what a settings change can run.
type Player interface {
	// Name is shown to the user.
	Name() string
	// args returns the command line for a validated stream URL, a cleaned
	// title (possibly "") and sanitized options.
	args(title, streamURL string, opts map[string]string) []string
}

// players maps lowercased executable names to the player they belong to.
// On macOS the name is the binary inside the app bundle.
var players = map[string]Player{
	"vlc":                 vlc{},
	"vlc.exe":             vlc{},
	"mpv":                 mpv{},
	"mpv.exe":             mpv{},
	"iina-cli":            iina{},
	"mpc-hc.exe":          urlOnly("MPC-HC"),
	"mpc-hc64.exe":        urlOnly("MPC-HC"),
	"potplayermini.exe":   urlOnly("PotPlayer"),
	"potplayermini64.exe": urlOnly("PotPlayer"),
}

// vlc passes the title and every sanitized option as item options
// (":name=value" after the URL): a running VLC that is handed the stream
// (see command) receives those with the URL, but not global
// "--name" options.
type vlc struct{}

func (vlc) Name() string { return "VLC" }

func (vlc) args(title, streamURL string, opts map[string]string) []string {
	args := []string{streamURL}
	if title != "" {
		args = append(args, ":meta-title="+title)
	}
	for _, k := range slices.Sorted(maps.Keys(opts)) {
		args = append(args, ":"+k+"="+opts[k])
	}
	return args
}

// mpvOptions maps sanitized VLC option names to mpv's. network-caching has
// no mpv equivalent and is dropped.
var mpvOptions = map[string]string{
	"audio-language":  "alang",
	"http-user-agent": "user-agent",
	"http-referrer":   "referrer",
}

// mpvArgs builds mpv options, each name prefixed with prefix.
func mpvArgs(prefix, title string, opts map[string]string) []string {
	var args []string
	if title != "" {
		args = append(args, prefix+"force-media-title="+title)
	}
	for _, k := range slices.Sorted(maps.Keys(opts)) {
		if name, ok := mpvOptions[k]; ok {
			args = append(args, prefix+name+"="+opts[k])
		}
	}
	return args
}

type mpv struct{}

func (mpv) Name() string { return "mpv" }

func (mpv) args(title, streamURL string, opts map[string]string) []string {
	return append(mpvArgs("--", title, opts), "--", streamURL)
}

// iina is driven through iina-cli, which forwards --mpv-* options to the
// mpv core inside IINA.
type iina struct{}

func (iina) Name() string { return "IINA" }

func (iina) args(title, streamURL string, opts map[string]string) []string {
	return append(mpvArgs("--mpv-", title, opts), streamURL)
}

// urlOnly is a player without a documented way to set the title or HTTP
// headers; it only receives the stream URL.
type urlOnly string

func (p urlOnly) Name() string { return string(p) }

func (urlOnly) args(_, streamURL string, _ map[string]string) []string {
	return []string{streamURL}
}

// ForExecutable returns the player an executable path belongs to.
func ForExecutable(path string) (Player, error) {
	p, ok := players[strings.ToLower(filepath.Base(path))]
	if !ok {
		return nil, fmt.Errorf("%q is not a supported player (%s)", path, supportedNames())
	}
	return p, nil
}

// WindowsExecutables returns the supported Windows executable names, sorted,
// for a file dialog filter.
func WindowsExecutables() []string {
	var out []string
	for name := range players {
		if strings.HasSuffix(name, ".exe") {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func supportedNames() string {
	var names []string
	for _, p := range players {
		names = append(names, p.Name())
	}
	slices.Sort(names)
	return strings.Join(slices.Compact(names), ", ")
}

// appBundle returns the macOS app bundle that contains exe, the inverse of
// bundleExecutable.
func appBundle(exe string) (string, bool) {
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	return bundle, strings.EqualFold(filepath.Ext(bundle), ".app")
}

// bundleExecutable returns the supported player binary inside a macOS app
// bundle, e.g. VLC.app/Contents/MacOS/VLC or IINA.app/Contents/MacOS/iina-cli.
func bundleExecutable(bundle string) (string, error) {
	dir := filepath.Join(bundle, "Contents", "MacOS")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("%q is not an app bundle", bundle)
	}
	for _, e := range entries {
		if _, ok := players[strings.ToLower(e.Name())]; ok {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("%q does not contain a supported player (%s)", bundle, supportedNames())
}
