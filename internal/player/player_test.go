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
		"audio-language":  "sv,en", // only from Prefs
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
	if got := SanitizeOptions(nil); got != nil {
		t.Errorf("SanitizeOptions(nil) = %v, want nil", got)
	}
}

func TestNormalizeLanguages(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		" , ":            "",
		"sv":             "sv",
		"SV, en  deu":    "sv,en,deu",
		"en,sv,en":       "en,sv",
		"fi,sv,en,de,fr": "fi,sv,en,de,fr",
	} {
		if got, err := NormalizeLanguages(in); err != nil || got != want {
			t.Errorf("NormalizeLanguages(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"e", "engl", "sv;en", "en-US", "--sout", "sv,\n", "aa,bb,cc,dd,ee,ff,gg,hh,ii,jj,kk"} {
		if got, err := NormalizeLanguages(in); err == nil {
			t.Errorf("NormalizeLanguages(%q) = %q, want error", in, got)
		}
	}
}

func TestCommand(t *testing.T) {
	const stream = testStream
	opts := map[string]string{
		"audio-language":  "de", // playlists cannot set it
		"http-user-agent": "UA",
		"http-referrer":   "http://r/",
		"network-caching": "1000",
		"sout":            "#file{dst=x}",
	}
	vlcArgs := []string{
		stream,
		":meta-title=BBC One HD",
		":audio-language=sv,en",
		":http-referrer=http://r/",
		":http-user-agent=UA",
		":network-caching=1000",
	}
	tests := []struct {
		exe  string
		want []string
	}{
		{filepath.Join("Program Files", "VideoLAN", "VLC", "vlc.exe"), vlcArgs},
		{"/usr/bin/mpv", []string{
			"--force-media-title=BBC One HD",
			"--alang=sv,en",
			"--referrer=http://r/",
			"--user-agent=UA",
			"--",
			stream,
		}},
		{"/Applications/IINA.app/Contents/MacOS/iina-cli", []string{
			"--mpv-force-media-title=BBC One HD",
			"--mpv-alang=sv,en",
			"--mpv-referrer=http://r/",
			"--mpv-user-agent=UA",
			stream,
		}},
		{filepath.Join("Program Files", "MPC-HC", "mpc-hc64.exe"), []string{stream}},
		{filepath.Join("Program Files", "DAUM", "PotPlayer", "PotPlayerMini64.exe"), []string{stream}},
	}
	for _, tt := range tests {
		cmd, err := command("linux", tt.exe, "BBC One\x07 HD", stream, opts, Prefs{AudioLanguages: "SV, en"})
		if err != nil {
			t.Errorf("command(%q) error: %v", tt.exe, err)
			continue
		}
		if got := cmd.Args[1:]; !reflect.DeepEqual(got, tt.want) {
			t.Errorf("command(%q) args = %q, want %q", tt.exe, got, tt.want)
		}
	}

	if _, err := command("linux", "vlc", "x", "--lua-intf=evil", nil, Prefs{}); err == nil {
		t.Error("command accepted an option as the stream URL")
	}
	if _, err := command("linux", "cmd.exe", "x", stream, nil, Prefs{}); err == nil {
		t.Error("command accepted an unsupported executable")
	}
	if _, err := command("linux", "vlc", "x", stream, nil, Prefs{AudioLanguages: "sv;--sout"}); err == nil {
		t.Error("command accepted invalid audio languages")
	}
}

func TestResolveExecutable(t *testing.T) {
	dir := t.TempDir()
	bundle := func(name string, bins ...string) string {
		app := filepath.Join(dir, name)
		macos := filepath.Join(app, "Contents", "MacOS")
		if err := os.MkdirAll(macos, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, b := range bins {
			if err := os.WriteFile(filepath.Join(macos, b), nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return app
	}

	iinaApp := bundle("IINA.app", "IINA", "iina-cli")
	if got, err := ResolveExecutable(iinaApp); err != nil || filepath.Base(got) != "iina-cli" {
		t.Errorf("ResolveExecutable(IINA.app) = %q, %v; want iina-cli", got, err)
	}
	if _, err := ResolveExecutable(bundle("Terminal.app", "Terminal")); err == nil {
		t.Error("ResolveExecutable accepted an unsupported app bundle")
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

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	vlc := filepath.Join(dir, "VLC.app", "Contents", "MacOS", "VLC")
	if err := os.MkdirAll(filepath.Dir(vlc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vlc, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing", "vlc")

	got, ok := locate([]locator{fixedPaths{missing, dir}, appBundles{dir}})
	if !ok || got != vlc {
		t.Errorf("locate = %q, %v; want %q, true (skipping missing files and directories)", got, ok, vlc)
	}
	if _, ok := locate([]locator{fixedPaths{missing}}); ok {
		t.Error("locate found a missing file")
	}
}
