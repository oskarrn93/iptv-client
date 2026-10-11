// Package player locates VLC and launches it for a stream.
//
// Everything that reaches the VLC command line originates from untrusted
// playlists, so this package is the security boundary: stream URLs must use
// an allowlisted scheme, and only a small allowlist of VLC options is passed
// through. Arguments are never interpreted by a shell.
package player

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// allowedSchemes are the stream URL schemes VLC may be asked to open.
// Notably absent: file, and anything VLC treats as a module/script.
var allowedSchemes = map[string]bool{
	"http": true, "https": true,
	"rtmp": true, "rtmps": true, "rtsp": true, "rtp": true,
	"udp": true, "mms": true, "mmsh": true,
}

// allowedOptions maps #EXTVLCOPT keys to a validator for their value.
// Everything else (sout, lua-*, extraintf, ...) is dropped.
var allowedOptions = map[string]func(string) bool{
	"http-user-agent": plainText,
	"http-referrer":   plainText,
	"network-caching": func(v string) bool {
		n, err := strconv.Atoi(v)
		return err == nil && n >= 0 && n <= 60000
	},
}

const maxOptionLen = 512

// ErrNotFound is returned when VLC cannot be located.
var ErrNotFound = errors.New("VLC was not found; install it from https://www.videolan.org or set its location in Settings")

// ValidateURL reports whether s is safe to hand to VLC as a stream URL.
func ValidateURL(s string) error {
	if s == "" {
		return errors.New("empty stream URL")
	}
	if strings.HasPrefix(s, "-") {
		return errors.New("stream URL must not start with '-'")
	}
	if hasControlOrSpace(s) {
		return errors.New("stream URL contains whitespace or control characters")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid stream URL: %w", err)
	}
	if !allowedSchemes[strings.ToLower(u.Scheme)] {
		return fmt.Errorf("stream URL scheme %q is not allowed", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("stream URL has no host")
	}
	return nil
}

// SanitizeOptions returns only the allowlisted options with valid values.
// Keys are lowercased. It never returns nil.
func SanitizeOptions(opts map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range opts {
		k = strings.ToLower(strings.TrimSpace(k))
		valid, ok := allowedOptions[k]
		if !ok || len(v) > maxOptionLen || !valid(v) {
			continue
		}
		out[k] = v
	}
	return out
}

// Args builds the VLC argument list for a stream. It re-validates its
// inputs, so callers cannot accidentally bypass the allowlists.
func Args(title, streamURL string, opts map[string]string) ([]string, error) {
	if err := ValidateURL(streamURL); err != nil {
		return nil, err
	}
	args := []string{}
	if t := cleanTitle(title); t != "" {
		args = append(args, "--meta-title="+t)
	}
	safe := SanitizeOptions(opts)
	for _, k := range slices.Sorted(maps.Keys(safe)) {
		args = append(args, "--"+k+"="+safe[k])
	}
	return append(args, streamURL), nil
}

// Launch starts VLC detached from this process and returns once it has
// started. The child is reaped in the background.
func Launch(vlcPath, title, streamURL string, opts map[string]string) error {
	args, err := Args(title, streamURL, opts)
	if err != nil {
		return err
	}
	cmd := exec.Command(vlcPath, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting VLC: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Find returns the VLC executable path. A non-empty override (from user
// settings) is used if it is a VLC executable; otherwise well-known
// locations for the current OS are searched.
func Find(override string) (string, error) {
	if override != "" {
		if err := ValidateExecutable(override); err != nil {
			return "", err
		}
		return override, nil
	}
	if p, ok := locate(locators()); ok {
		return p, nil
	}
	return "", ErrNotFound
}

// ValidateExecutable checks that path is an existing file named like the VLC
// executable. This stops a settings change from turning "play" into running
// an arbitrary program.
func ValidateExecutable(path string) error {
	base := strings.ToLower(filepath.Base(path))
	if base != "vlc" && base != "vlc.exe" {
		return fmt.Errorf("%q is not a VLC executable (expected vlc or vlc.exe)", path)
	}
	if !isFile(path) {
		return fmt.Errorf("%q does not exist", path)
	}
	return nil
}

// ResolveExecutable maps a path the user picked to the VLC executable (a
// macOS VLC.app bundle to the binary inside it) and validates it.
func ResolveExecutable(picked string) (string, error) {
	if strings.HasSuffix(strings.ToLower(picked), ".app") {
		picked = filepath.Join(picked, "Contents", "MacOS", "VLC")
	}
	return picked, ValidateExecutable(picked)
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func plainText(v string) bool {
	return v != "" && !strings.HasPrefix(v, "-") && !hasControl(v)
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func hasControl(s string) bool { return strings.ContainsFunc(s, isControl) }

func hasControlOrSpace(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r == ' ' || isControl(r) })
}

// cleanTitle removes control characters and bounds the length.
func cleanTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}
