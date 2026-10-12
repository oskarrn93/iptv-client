// Package player locates a media player (VLC by default) and launches it for
// a stream.
//
// Everything that reaches the player's command line originates from
// untrusted playlists, so this package is the security boundary: only known
// players are started, stream URLs must use an allowlisted scheme, and only
// a small allowlist of options is passed through. Arguments are never
// interpreted by a shell.
package player

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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

// ErrNotFound is returned when no player is chosen and VLC cannot be located.
var ErrNotFound = errors.New("VLC was not found; install it from https://www.videolan.org or choose a player in Settings")

// ValidateURL reports whether s is safe to hand to a player as a stream URL.
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
	if len(opts) == 0 { // most channels have none
		return nil
	}
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

// Args builds the argument list for playing a stream with the player at
// exe. It re-validates its inputs, so callers cannot accidentally bypass the
// allowlists.
func Args(exe, title, streamURL string, opts map[string]string) ([]string, error) {
	p, err := ForExecutable(exe)
	if err != nil {
		return nil, err
	}
	if err := ValidateURL(streamURL); err != nil {
		return nil, err
	}
	return p.args(cleanTitle(title), streamURL, SanitizeOptions(opts)), nil
}

// Launch starts the player at exe detached from this process and returns
// once it has started. The child is reaped in the background.
func Launch(exe, title, streamURL string, opts map[string]string) error {
	args, err := Args(exe, title, streamURL, opts)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", filepath.Base(exe), err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Find returns the player executable path. A non-empty override (from user
// settings) is used if it is a supported player; otherwise well-known VLC
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

// ValidateExecutable checks that path is an existing file named like a
// supported player's executable. This stops a settings change from turning
// "play" into running an arbitrary program.
func ValidateExecutable(path string) error {
	if _, err := ForExecutable(path); err != nil {
		return err
	}
	if !isFile(path) {
		return fmt.Errorf("%q does not exist", path)
	}
	return nil
}

// ResolveExecutable maps a path the user picked to the player executable (a
// macOS app bundle to the supported binary inside it) and validates it.
func ResolveExecutable(picked string) (string, error) {
	if strings.HasSuffix(strings.ToLower(picked), ".app") {
		exe, err := bundleExecutable(picked)
		if err != nil {
			return "", err
		}
		picked = exe
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
