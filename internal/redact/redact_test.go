package redact

import (
	"regexp"
	"strings"
	"testing"
)

func TestURLs(t *testing.T) {
	r := newRedactor("")
	tests := []struct{ in, want string }{
		{"http://provider.tv/get.php?username=u&password=p", "http://provider.tv/…"},
		{"http://user:pass@provider.tv:8080", "http://provider.tv:8080/…"},
		{"http://provider.tv/", "http://provider.tv/"},
		{"https://provider.tv", "https://provider.tv"},
		{"rtmp://1.2.3.4:1935/live/key", "rtmp://1.2.3.4:1935/…"},
		// Xtream-style credentials in the path, without a scheme.
		{"provider.tv:8080/live/user/pass/1001.ts", "provider.tv:8080/…"},
		{"localhost:8080/user/pass", "localhost:8080/…"},
		// Inside free text: only the URL is redacted.
		{`invalid stream URL: parse "http://u:p@h/x": invalid port`, `invalid stream URL: parse "http://h/…": invalid port`},
		{"Get http://h/a/b: EOF", "Get http://h/…: EOF"},
		{`{"args":["name","http://h/u/p.m3u"]}`, `{"args":["name","http://h/…"]}`},
		{"a http://h/1 and https://g/2", "a http://h/… and https://g/…"},
		// App asset URLs and module paths in stack traces are kept.
		{"at f (wails://wails/assets/index-x.js:1:200)", "at f (wails://wails/assets/index-x.js:1:200)"},
		{"at f (http://wails.localhost/assets/index-x.js:1:200)", "at f (http://wails.localhost/assets/index-x.js:1:200)"},
		{"github.com/wailsapp/wails/v2/internal/x.go:42", "github.com/wailsapp/wails/v2/internal/x.go:42"},
		{"http://u:p@wails.localhost/x", "http://wails.localhost/…"},
		// Found in review: allowlisted hosts, cut-off user info, quotes in
		// passwords, JSON-escaped slashes.
		{"https://github.com/acme/raw/list.m3u?token=SECRET", "https://github.com/…"},
		{"http://wails.localhost/x?password=SECRET", "http://wails.localhost/x?…"},
		{"http://user:SECRET", "http://…"},
		{"http://user:SEC…", "http://…"},
		{"http://user:p'SECRET@provider.tv/live/1.ts", "http://provider.tv/…"},
		{`parse "http://user:p\"SECRET@provider.tv/x"`, `parse "http://provider.tv/…"`},
		{`{"u":"http:\/\/provider.tv\/get.php?password=SECRET"}`, `{"u":"http:\/\/…"}`},
		// Not URLs.
		{"imported=5 took=1.2s", "imported=5 took=1.2s"},
		{"dial tcp 1.2.3.4:443: connection refused", "dial tcp 1.2.3.4:443: connection refused"},
		{"iptv-client/internal/playlist.(*Service).Refresh", "iptv-client/internal/playlist.(*Service).Refresh"},
	}
	for _, tt := range tests {
		if got := r.String(tt.in); got != tt.want {
			t.Errorf("String(%q)\n got %q\nwant %q", tt.in, got, tt.want)
		}
	}
}

func TestHome(t *testing.T) {
	r := newRedactor(`C:\Users\Alice`)
	tests := []struct{ in, want string }{
		{`open C:\Users\Alice\lists\a.m3u: not found`, `open ~\lists\a.m3u: not found`},
		{`"C:\\Users\\Alice\\lists\\a.m3u"`, `"~\\lists\\a.m3u"`},
		{`C:/Users/Alice/x`, `~/x`},
		{`C:\Users\Bob\x`, `C:\Users\Bob\x`},
	}
	for _, tt := range tests {
		if got := r.String(tt.in); got != tt.want {
			t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := newRedactor("/Users/alice").String("/Users/alice/Library/x"); got != "~/Library/x" {
		t.Errorf("POSIX home: got %q", got)
	}
}

var validHost = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// FuzzString checks that credentials never survive redaction, wherever a
// URL carrying them appears in the text.
func FuzzString(f *testing.F) {
	f.Add("before ", " after", "provider.tv:8080")
	f.Add(`parse "`, `": bad`, "1.2.3.4")
	f.Add("", "", "h")
	f.Fuzz(func(t *testing.T, before, after, host string) {
		// Only host names a URL can have: whitespace, quotes and brackets end
		// a URL in text, leaving a bare path that is not recognized as one.
		if !validHost.MatchString(host) || appHosts[strings.ToLower(host)] {
			return
		}
		const secret = "S3cr3tPa55"
		for _, u := range []string{
			"http://user:" + secret + "@" + host + "/x",
			"http://" + host + "/live/user/" + secret + "/1.ts",
			"http://" + host + "/get.php?username=u&password=" + secret,
		} {
			in := before + " " + u + " " + after
			if strings.Contains(before, secret) || strings.Contains(after, secret) {
				return
			}
			if got := newRedactor("").String(in); strings.Contains(got, secret) {
				t.Fatalf("secret survived: %q -> %q", in, got)
			}
		}
	})
}
