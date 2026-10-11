package library

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"iptv-client/internal/store"
)

const playlistBody = `#EXTM3U
#EXTINF:-1 tvg-id="a" group-title="News",Good One
#EXTVLCOPT:http-referrer=http://ref/
#EXTVLCOPT:sout=#file{dst=evil}
http://example.com/1.ts
#EXTINF:-1 group-title="News",Option Injection
--extraintf=lua
#EXTINF:-1 group-title="News",Local File
file:///C:/Windows/System32/calc.exe
#EXTINF:-1 group-title="Sport",Good Two
https://example.com/2.m3u8
`

func newLib(t *testing.T) *Library {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s)
}

func allChannels(t *testing.T, l *Library, pid int64) []store.Channel {
	t.Helper()
	ctx := context.Background()
	ids, err := l.Store.ChannelIDs(ctx, store.ChannelFilter{PlaylistID: pid})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := l.Store.ChannelsByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// server serves body (switchable) with an ETag and counts requests.
type server struct {
	*httptest.Server
	body  atomic.Value
	hits  atomic.Int32
	etag  atomic.Value
	agent atomic.Value
}

func newServer(t *testing.T, body string) *server {
	s := &server{}
	s.body.Store(body)
	s.etag.Store(`"1"`)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.agent.Store(r.Header.Get("User-Agent"))
		et := s.etag.Load().(string)
		if r.Header.Get("If-None-Match") == et {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", et)
		io.WriteString(w, s.body.Load().(string))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestAddPlaylistFiltersUnsafeEntries(t *testing.T) {
	ctx := context.Background()
	l := newLib(t)
	srv := newServer(t, playlistBody)

	var progressed int
	p, res, err := l.AddPlaylist(ctx, "", srv.URL+"/get.php?username=u&password=p", func(n int) { progressed = n })
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.Skipped != 2 || progressed != 2 {
		t.Errorf("result = %+v, progress = %d", res, progressed)
	}
	if p.Name == "" || p.ChannelCount != 2 || p.ETag != `"1"` {
		t.Errorf("playlist = %+v", p)
	}
	cs := allChannels(t, l, p.ID)
	if len(cs) != 2 || cs[0].Name != "Good One" || cs[1].Name != "Good Two" {
		t.Fatalf("channels = %+v", cs)
	}
	if want := map[string]string{"http-referrer": "http://ref/"}; !reflect.DeepEqual(cs[0].Options, want) {
		t.Errorf("stored options = %v, want only allowlisted %v", cs[0].Options, want)
	}
}

func TestRefreshPolicy(t *testing.T) {
	ctx := context.Background()
	l := newLib(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l.Now = func() time.Time { return now }
	srv := newServer(t, playlistBody)

	p, _, err := l.AddPlaylist(ctx, "P", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The store records real wall-clock time; move our clock relative to it.
	stored, _ := l.Store.GetPlaylist(ctx, p.ID)
	now = stored.LastRefreshedAt.Add(10 * time.Minute)

	_, err = l.Refresh(ctx, p.ID, false, nil)
	var tooSoon *TooSoonError
	if !errors.As(err, &tooSoon) || tooSoon.Remaining != 50*time.Minute {
		t.Fatalf("err = %v, want TooSoonError(50m)", err)
	}
	hits := srv.hits.Load()

	// Forced refresh goes through; unchanged ETag means 304 and no rewrite.
	res, err := l.Refresh(ctx, p.ID, true, nil)
	if err != nil || !res.NotModified {
		t.Fatalf("forced refresh = %+v, %v", res, err)
	}
	if srv.hits.Load() != hits+1 {
		t.Error("forced refresh did not hit server")
	}

	// After the interval and with new content, channels are replaced.
	now = now.Add(2 * time.Hour)
	srv.etag.Store(`"2"`)
	srv.body.Store("#EXTM3U\n#EXTINF:-1,Only\nhttp://example.com/only\n")
	if res, err = l.Refresh(ctx, p.ID, false, nil); err != nil || res.Imported != 1 {
		t.Fatalf("refresh = %+v, %v", res, err)
	}
	if cs := allChannels(t, l, p.ID); len(cs) != 1 || cs[0].Name != "Only" {
		t.Errorf("channels = %+v", cs)
	}
}

func TestRefreshKeepsOldDataOnBadContent(t *testing.T) {
	ctx := context.Background()
	l := newLib(t)
	srv := newServer(t, playlistBody)
	p, _, err := l.AddPlaylist(ctx, "P", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"<html>Account expired</html>", "#EXTM3U\n", "#EXTM3U\n#EXTINF:-1,x\n--evil\n"} {
		srv.etag.Store(`"bad` + string(rune('0'+i)) + `"`)
		srv.body.Store(body)
		if _, err := l.Refresh(ctx, p.ID, true, nil); !errors.Is(err, ErrNoChannels) {
			t.Errorf("body %q: err = %v, want ErrNoChannels", body, err)
		}
		if cs := allChannels(t, l, p.ID); len(cs) != 2 {
			t.Errorf("body %q: channels wiped (%d left)", body, len(cs))
		}
	}
}

func TestAddPlaylistFailureSavesNothing(t *testing.T) {
	ctx := context.Background()
	l := newLib(t)
	srv := newServer(t, "not a playlist <html>")
	if _, _, err := l.AddPlaylist(ctx, "x", srv.URL, nil); err == nil {
		t.Fatal("expected error")
	}
	for _, src := range []string{"", "ftp://x/y", filepath.Join(t.TempDir(), "missing.m3u"), t.TempDir()} {
		if _, _, err := l.AddPlaylist(ctx, "x", src, nil); err == nil {
			t.Errorf("AddPlaylist(%q) succeeded", src)
		}
	}
	if ps, _ := l.Store.ListPlaylists(ctx); len(ps) != 0 {
		t.Errorf("playlists saved after failures: %+v", ps)
	}
}

func TestHLSSource(t *testing.T) {
	ctx := context.Background()
	l := newLib(t)
	srv := newServer(t, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nlow.m3u8\n")
	p, res, err := l.AddPlaylist(ctx, "My Stream", srv.URL+"/master.m3u8", nil)
	if err != nil || !res.HLS || res.Imported != 1 {
		t.Fatalf("add = %+v, %v", res, err)
	}
	cs := allChannels(t, l, p.ID)
	if len(cs) != 1 || cs[0].Name != "My Stream" || cs[0].URL != srv.URL+"/master.m3u8" {
		t.Errorf("channels = %+v", cs)
	}

	f := filepath.Join(t.TempDir(), "master.m3u8")
	os.WriteFile(f, []byte("#EXTM3U\n#EXT-X-VERSION:3\n"), 0o600)
	if _, _, err := l.AddPlaylist(ctx, "", f, nil); err == nil {
		t.Error("HLS file source accepted")
	}
}

func TestFileSourceAndUserAgent(t *testing.T) {
	ctx := context.Background()
	l := newLib(t)
	f := filepath.Join(t.TempDir(), "My List.m3u")
	os.WriteFile(f, []byte(playlistBody), 0o600)
	p, res, err := l.AddPlaylist(ctx, "", f, nil)
	if err != nil || res.Imported != 2 || p.Name != "My List" || p.SourceType != store.SourceFile {
		t.Fatalf("add = %+v %+v %v", p, res, err)
	}
	// File playlists have no refresh rate limit.
	if _, err := l.Refresh(ctx, p.ID, false, nil); err != nil {
		t.Errorf("file refresh: %v", err)
	}

	if err := l.SetUserAgent(ctx, "Custom/2"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetUserAgent(ctx, "a\r\nX-Injected: 1"); err == nil {
		t.Error("header injection accepted")
	}
	srv := newServer(t, playlistBody)
	if _, _, err := l.AddPlaylist(ctx, "u", srv.URL, nil); err != nil {
		t.Fatal(err)
	}
	if got := srv.agent.Load(); got != "Custom/2" {
		t.Errorf("User-Agent = %v", got)
	}
}

func TestSetVLCPath(t *testing.T) {
	ctx := context.Background()
	l := newLib(t)
	dir := t.TempDir()
	evil := filepath.Join(dir, "cmd.exe")
	os.WriteFile(evil, nil, 0o755)
	if err := l.SetVLCPath(ctx, evil); err == nil {
		t.Error("non-VLC path accepted")
	}
	vlc := filepath.Join(dir, "vlc.exe")
	os.WriteFile(vlc, nil, 0o755)
	if err := l.SetVLCPath(ctx, vlc); err != nil {
		t.Fatal(err)
	}
	if got, err := l.VLCPath(ctx); err != nil || got != vlc {
		t.Errorf("VLCPath = %q, %v", got, err)
	}
	if err := l.SetVLCPath(ctx, ""); err != nil {
		t.Fatal(err)
	}
}
