package player

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

const testStream = "http://example.com/1.ts"

func TestCommandWindows(t *testing.T) {
	for _, tt := range []struct {
		exe  string
		want []string
	}{
		{"vlc.exe", []string{"vlc.exe", "--one-instance", testStream, ":meta-title=BBC One"}},
		{"mpv.exe", []string{"mpv.exe", "--force-media-title=BBC One", "--", testStream}},
	} {
		cmd, err := command("windows", tt.exe, "BBC One", testStream, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cmd.Args, tt.want) {
			t.Errorf("%s: command = %q, want %q", tt.exe, cmd.Args, tt.want)
		}
	}
}

func TestCommandMacVLCBundle(t *testing.T) {
	bundle := filepath.Join("Applications", "VLC.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "VLC")
	cmd, err := command("darwin", exe, "BBC One", testStream, map[string]string{
		"http-user-agent": "UA x",
		"http-referrer":   "http://r/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) != 4 || cmd.Args[0] != "/usr/bin/open" || cmd.Args[2] != bundle {
		t.Fatalf("command = %q, want open -a %s <playlist>", cmd.Args, bundle)
	}
	file := cmd.Args[3]
	t.Cleanup(func() { os.Remove(file) })
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("playlist mode = %v, want 0600", perm)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "#EXTM3U\n" +
		"#EXTINF:-1,BBC One\n" +
		"#EXTVLCOPT:http-referrer=http://r/\n" +
		"#EXTVLCOPT:http-user-agent=UA x\n" +
		testStream + "\n"
	if string(data) != want {
		t.Errorf("playlist = %q, want %q", data, want)
	}
}

func TestCommandDirect(t *testing.T) {
	for _, tt := range []struct {
		goos, exe string
		p         Player
	}{
		{"darwin", "/opt/homebrew/bin/vlc", vlc{}}, // not inside an app bundle
		{"darwin", "/Applications/IINA.app/Contents/MacOS/iina-cli", iina{}},
		{"linux", "/usr/bin/vlc", vlc{}},
	} {
		cmd, err := command(tt.goos, tt.exe, "", testStream, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := append([]string{tt.exe}, tt.p.args("", testStream, nil)...); !slices.Equal(cmd.Args, want) {
			t.Errorf("%s %q: command = %q, want %q", tt.goos, tt.exe, cmd.Args, want)
		}
	}
}

func TestVLCPlaylistNoTitle(t *testing.T) {
	if got, want := vlcPlaylist("", "http://h/1", nil), "#EXTM3U\nhttp://h/1\n"; got != want {
		t.Errorf("vlcPlaylist = %q, want %q", got, want)
	}
}
