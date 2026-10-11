package player

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidateURL(t *testing.T) {
	ok := []string{
		"http://example.com/live/1.ts",
		"HTTPS://example.com/a.m3u8?token=x",
		"rtmp://example.com/app/stream",
		"rtsp://192.168.1.10:554/stream",
		"udp://@239.0.0.1:1234",
		"rtp://239.0.0.1:5004",
		"mms://example.com/x",
	}
	bad := []string{
		"",
		"--extraintf=lua",
		"-I lua",
		"file:///C:/Windows/System32/cmd.exe",
		"javascript:alert(1)",
		"vlc://quit",
		"screen://",
		"http://example.com/a b",
		"http://example.com/a\nhttp://b",
		"http:///nohost",
		"example.com/relative",
		"segment0.ts",
	}
	for _, s := range ok {
		if err := ValidateURL(s); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidateURL(s); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", s)
		}
	}
}

func TestSanitizeOptions(t *testing.T) {
	in := map[string]string{
		"http-user-agent": "Mozilla/5.0 (X11)",
		"HTTP-Referrer":   "http://site.example.com/",
		"network-caching": "1500",
		"sout":            `#file{dst=C:\evil.bat}`,
		"lua-intf":        "x",
		"extraintf":       "http",
	}
	want := map[string]string{
		"http-user-agent": "Mozilla/5.0 (X11)",
		"http-referrer":   "http://site.example.com/",
		"network-caching": "1500",
	}
	if got := SanitizeOptions(in); !reflect.DeepEqual(got, want) {
		t.Errorf("SanitizeOptions = %v, want %v", got, want)
	}

	rejected := []map[string]string{
		{"network-caching": "abc"},
		{"network-caching": "-1"},
		{"network-caching": "99999999"},
		{"http-user-agent": "--extraintf=lua"},
		{"http-user-agent": "a\nb"},
		{"http-referrer": ""},
	}
	for _, m := range rejected {
		if got := SanitizeOptions(m); len(got) != 0 {
			t.Errorf("SanitizeOptions(%v) = %v, want empty", m, got)
		}
	}
	if SanitizeOptions(nil) == nil {
		t.Error("SanitizeOptions(nil) returned nil map")
	}
}

func TestArgs(t *testing.T) {
	got, err := Args("BBC One\x07 HD", "http://example.com/1.ts", map[string]string{
		"http-user-agent": "UA",
		"http-referrer":   "http://r/",
		"sout":            "#file{dst=x}",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--meta-title=BBC One HD",
		"--http-referrer=http://r/",
		"--http-user-agent=UA",
		"http://example.com/1.ts",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %q, want %q", got, want)
	}

	if _, err := Args("x", "--lua-intf=evil", nil); err == nil {
		t.Error("Args accepted an option as the stream URL")
	}
}

func TestValidateExecutable(t *testing.T) {
	dir := t.TempDir()
	vlc := filepath.Join(dir, "vlc.exe")
	other := filepath.Join(dir, "cmd.exe")
	for _, p := range []string{vlc, other} {
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateExecutable(vlc); err != nil {
		t.Errorf("ValidateExecutable(vlc.exe) = %v", err)
	}
	if err := ValidateExecutable(other); err == nil {
		t.Error("ValidateExecutable accepted cmd.exe")
	}
	if err := ValidateExecutable(filepath.Join(dir, "vlc")); err == nil {
		t.Error("ValidateExecutable accepted a missing file")
	}
	if _, err := Find(other); err == nil {
		t.Error("Find accepted a non-VLC override")
	}
}
