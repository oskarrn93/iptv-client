// Package redact removes credentials and personal paths from free text
// before it is logged. Playlist and stream URLs usually carry the provider
// account in their user info, path or query, and file paths under the home
// directory contain the user's name.
//
// It is a backstop: code should not log URLs or paths in the first place.
package redact

import (
	"os"
	"regexp"
	"runtime"
	"strings"
)

// urlish matches, leftmost first:
//   - scheme://anything up to whitespace, a quote or a bracket
//   - scheme-less host[:port]/path (e.g. "provider.tv:8080/live/user/pass/1.ts")
//   - host:port/path without a dot (e.g. "localhost:8080/user/pass")
var urlish = regexp.MustCompile(
	`[A-Za-z][A-Za-z0-9+.\-]*://[^\s"'<>()\[\]{}` + "`" + `]*` +
		`|(?:[A-Za-z0-9\-]+\.)+[A-Za-z0-9\-]+(?::[0-9]{1,5})?/[^\s"'<>()\[\]{}` + "`" + `]*` +
		`|[A-Za-z0-9\-]+:[0-9]{1,5}/[^\s"'<>()\[\]{}` + "`" + `]*`)

// userInfo matches the user info of a URL, up to the last "@" before the
// host. It is removed before urlish runs because a password may contain
// characters (quotes, escaped quotes) that end a urlish match early.
var userInfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^\s/?#@]*@`)

// escapedURL matches URLs with JSON-escaped slashes (http:\/\/...).
var escapedURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*):\\/\\/[^\s"'<>]*`)

// appHosts serve the embedded frontend; their paths appear in JavaScript
// stack traces and are kept (never a query, fragment or user info).
var appHosts = map[string]bool{
	"wails":           true, // wails://wails/... (macOS)
	"wails.localhost": true, // http://wails.localhost/... (Windows)
}

// codeHosts appear without a scheme as module paths in Go stack traces;
// those are kept. With a scheme they are ordinary URLs.
var codeHosts = map[string]bool{
	"github.com":  true,
	"golang.org":  true,
	"modernc.org": true,
}

// Redactor redacts text for one home directory.
type Redactor struct {
	home *regexp.Regexp // nil if the home directory is unknown
}

// New returns a Redactor that replaces home with "~". An empty home disables
// path redaction.
func New(home string) *Redactor {
	r := &Redactor{}
	home = strings.TrimRight(home, `/\`)
	if len(home) < 2 {
		return r
	}
	// The home directory as written natively, with forward slashes, and with
	// the doubled backslashes of %q or JSON quoting.
	variants := []string{home, strings.ReplaceAll(home, `\`, `/`), strings.ReplaceAll(home, `\`, `\\`)}
	for i, v := range variants {
		variants[i] = regexp.QuoteMeta(v)
	}
	flags := ""
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		flags = "(?i)" // case-insensitive file systems
	}
	r.home = regexp.MustCompile(flags + `(?:` + strings.Join(variants, "|") + `)`)
	return r
}

var std = func() *Redactor {
	home, _ := os.UserHomeDir()
	return New(home)
}()

// String redacts s with the current user's home directory.
func String(s string) string { return std.String(s) }

// String returns s with every URL-like part reduced to its scheme and host
// and the home directory replaced by "~".
func (r *Redactor) String(s string) string {
	if strings.Contains(s, `:\/\/`) {
		s = escapedURL.ReplaceAllString(s, `${1}:\/\/…`)
	}
	if strings.Contains(s, "@") {
		s = userInfo.ReplaceAllString(s, "${1}…@")
	}
	if strings.ContainsAny(s, ":/") {
		s = urlish.ReplaceAllStringFunc(s, redactURL)
	}
	if r.home != nil {
		s = r.home.ReplaceAllLiteralString(s, "~")
	}
	return s
}

// redactURL reduces one URL-like match to scheme://host/… or host/….
func redactURL(m string) string {
	// Punctuation that ends a sentence or an error prefix ("Get http://h/x: EOF")
	// is not part of the URL.
	trimmed := strings.TrimRight(m, ".,;:!")
	return redactTrimmed(trimmed) + m[len(trimmed):]
}

func redactTrimmed(m string) string {
	scheme, rest, hasScheme := strings.Cut(m, "://")
	if !hasScheme {
		rest = m
	}
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	hadUserInfo := false
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority, hadUserInfo = authority[i+1:], true
	}
	host, port, hasPort := authority, "", false
	if !strings.HasPrefix(authority, "[") {
		host, port, hasPort = strings.Cut(authority, ":")
	}
	// Not a port: likely user info cut off before its "@" ("http://user:pass").
	if hasPort && !isPort(port) {
		return scheme + "://…"
	}
	host = strings.ToLower(host)
	switch {
	case !hasScheme && codeHosts[host]:
		return m
	case hasScheme && appHosts[host] && !hadUserInfo:
		if i := strings.IndexAny(m, "?#"); i >= 0 {
			return m[:i] + "?…"
		}
		return m
	}
	out := authority
	if hasScheme {
		out = scheme + "://" + authority
	}
	switch {
	case hadUserInfo || (tail != "" && tail != "/"):
		out += "/…"
	case tail == "/":
		out += "/"
	}
	return out
}

func isPort(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
