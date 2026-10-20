package playlist

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"iptv-client/internal/channel"
	"iptv-client/internal/database"
	"iptv-client/internal/epg"
	"iptv-client/internal/setting"
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

// services is the Service under test plus the ones used to inspect its
// results.
type services struct {
	db        *database.DB
	Playlists *Service
	Channels  *channel.Service
	Settings  *setting.Service
}

func newServices(t *testing.T) *services {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	settings := setting.NewService(db)
	return &services{
		db:        db,
		Playlists: NewService(db, settings),
		Channels:  channel.NewService(db, settings),
		Settings:  settings,
	}
}

func allChannels(t *testing.T, svc *services, pid int64) []channel.Channel {
	t.Helper()
	ctx := context.Background()
	ids, err := svc.Channels.IDs(ctx, channel.Filter{PlaylistID: pid})
	if err != nil {
		t.Fatal(err)
	}
	cs := make([]channel.Channel, len(ids))
	for i, id := range ids {
		if cs[i], err = svc.Channels.Get(ctx, id); err != nil {
			t.Fatal(err)
		}
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
	svc := newServices(t)
	srv := newServer(t, playlistBody)

	var progressed int
	p, res, err := svc.Playlists.Add(ctx, "", srv.URL+"/get.php?username=u&password=p", func(n int) { progressed = n })
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.Skipped != 2 || progressed != 2 {
		t.Errorf("result = %+v, progress = %d", res, progressed)
	}
	if p.Name == "" || p.ChannelCount != 2 || p.ETag != `"1"` {
		t.Errorf("playlist = %+v", p)
	}
	cs := allChannels(t, svc, p.ID)
	if len(cs) != 2 || cs[0].Name != "Good One" || cs[1].Name != "Good Two" {
		t.Fatalf("channels = %+v", cs)
	}
	if want := map[string]string{"http-referrer": "http://ref/"}; !reflect.DeepEqual(cs[0].Options, want) {
		t.Errorf("stored options = %v, want only allowlisted %v", cs[0].Options, want)
	}
}

func TestRefreshPolicy(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.Playlists.Now = func() time.Time { return now }
	srv := newServer(t, playlistBody)

	p, _, err := svc.Playlists.Add(ctx, "P", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Move the clock relative to the recorded refresh time.
	stored, _ := svc.Playlists.Get(ctx, p.ID)
	now = stored.LastRefreshedAt.Add(10 * time.Minute)

	_, err = svc.Playlists.Refresh(ctx, p.ID, false, nil)
	var tooSoon *TooSoonError
	if !errors.As(err, &tooSoon) || tooSoon.Remaining != 50*time.Minute {
		t.Fatalf("err = %v, want TooSoonError(50m)", err)
	}
	hits := srv.hits.Load()

	// Forced refresh goes through; unchanged ETag means 304 and no rewrite.
	res, err := svc.Playlists.Refresh(ctx, p.ID, true, nil)
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
	if res, err = svc.Playlists.Refresh(ctx, p.ID, false, nil); err != nil || res.Imported != 1 {
		t.Fatalf("refresh = %+v, %v", res, err)
	}
	if cs := allChannels(t, svc, p.ID); len(cs) != 1 || cs[0].Name != "Only" {
		t.Errorf("channels = %+v", cs)
	}
}

func TestRefreshKeepsOldDataOnBadContent(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	srv := newServer(t, playlistBody)
	p, _, err := svc.Playlists.Add(ctx, "P", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"<html>Account expired</html>", "#EXTM3U\n", "#EXTM3U\n#EXTINF:-1,x\n--evil\n"} {
		srv.etag.Store(`"bad` + string(rune('0'+i)) + `"`)
		srv.body.Store(body)
		if _, err := svc.Playlists.Refresh(ctx, p.ID, true, nil); !errors.Is(err, ErrNoChannels) {
			t.Errorf("body %q: err = %v, want ErrNoChannels", body, err)
		}
		if cs := allChannels(t, svc, p.ID); len(cs) != 2 {
			t.Errorf("body %q: channels wiped (%d left)", body, len(cs))
		}
	}
}

func TestAddPlaylistFailureSavesNothing(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	srv := newServer(t, "not a playlist <html>")
	if _, _, err := svc.Playlists.Add(ctx, "x", srv.URL, nil); err == nil {
		t.Fatal("expected error")
	}
	for _, src := range []string{"", "ftp://x/y", filepath.Join(t.TempDir(), "missing.m3u"), t.TempDir()} {
		if _, _, err := svc.Playlists.Add(ctx, "x", src, nil); err == nil {
			t.Errorf("AddPlaylist(%q) succeeded", src)
		}
	}
	if ps, _ := svc.Playlists.List(ctx); len(ps) != 0 {
		t.Errorf("playlists saved after failures: %+v", ps)
	}
}

func TestHLSSource(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	srv := newServer(t, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nlow.m3u8\n")
	p, res, err := svc.Playlists.Add(ctx, "My Stream", srv.URL+"/master.m3u8", nil)
	if err != nil || !res.HLS || res.Imported != 1 {
		t.Fatalf("add = %+v, %v", res, err)
	}
	cs := allChannels(t, svc, p.ID)
	if len(cs) != 1 || cs[0].Name != "My Stream" || cs[0].URL != srv.URL+"/master.m3u8" {
		t.Errorf("channels = %+v", cs)
	}

	f := filepath.Join(t.TempDir(), "master.m3u8")
	os.WriteFile(f, []byte("#EXTM3U\n#EXT-X-VERSION:3\n"), 0o600)
	if _, _, err := svc.Playlists.Add(ctx, "", f, nil); err == nil {
		t.Error("HLS file source accepted")
	}
}

func TestFileSourceAndUserAgent(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	f := filepath.Join(t.TempDir(), "My List.m3u")
	os.WriteFile(f, []byte(playlistBody), 0o600)
	p, res, err := svc.Playlists.Add(ctx, "", f, nil)
	if err != nil || res.Imported != 2 || p.Name != "My List" || p.SourceType != SourceFile {
		t.Fatalf("add = %+v %+v %v", p, res, err)
	}
	// File playlists have no refresh rate limit.
	if _, err := svc.Playlists.Refresh(ctx, p.ID, false, nil); err != nil {
		t.Errorf("file refresh: %v", err)
	}

	if err := svc.Settings.SetUserAgent(ctx, "Custom/2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Settings.SetUserAgent(ctx, "a\r\nX-Injected: 1"); err == nil {
		t.Error("header injection accepted")
	}
	srv := newServer(t, playlistBody)
	if _, _, err := svc.Playlists.Add(ctx, "u", srv.URL, nil); err != nil {
		t.Fatal(err)
	}
	if got := srv.agent.Load(); got != "Custom/2" {
		t.Errorf("User-Agent = %v", got)
	}
}

// feed returns a produce function for replaceChannels.
func feed(cs ...channel.Input) func(func(channel.Input) error) (string, error) {
	return func(insert func(channel.Input) error) (string, error) {
		for _, c := range cs {
			if err := insert(c); err != nil {
				return "", err
			}
		}
		return "", nil
	}
}

func channelNames(t *testing.T, svc *services, f channel.Filter) []string {
	t.Helper()
	ctx := context.Background()
	ids, err := svc.Channels.IDs(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := svc.Channels.ByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

var sample = []channel.Input{
	{Name: "BBC One HD", URL: "http://x/user/pass/1", Group: "UK", TvgID: "bbc1.uk"},
	{Name: "BBC One SD", URL: "http://x/user/pass/2", Group: "UK", TvgID: "bbc1.uk"},
	{Name: "CNN International", URL: "http://x/user/pass/3", Group: "News"},
	{Name: "Loose", URL: "http://x/user/pass/5"},
}

func addEmptyPlaylist(t *testing.T, svc *services, name string) int64 {
	t.Helper()
	id, err := svc.Playlists.repo.Create(context.Background(), name, SourceURL, "http://e/"+name, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReplaceChannelsRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	pid := addEmptyPlaylist(t, svc, "p")
	if err := svc.Playlists.replaceChannels(ctx, pid, "old", "", feed(sample...)); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("parse failed")
	err := svc.Playlists.replaceChannels(ctx, pid, "new", "", func(insert func(channel.Input) error) (string, error) {
		insert(channel.Input{Name: "Half", URL: "http://x/h"})
		return "", boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got := channelNames(t, svc, channel.Filter{PlaylistID: pid}); len(got) != len(sample) {
		t.Errorf("after failed replace got %q, want original %d", got, len(sample))
	}
	if got := channelNames(t, svc, channel.Filter{PlaylistID: pid, Query: "half"}); len(got) != 0 {
		t.Errorf("FTS has rolled-back row: %q", got)
	}
	if p, _ := svc.Playlists.Get(ctx, pid); p.ETag != "old" || p.ChannelCount != len(sample) {
		t.Errorf("playlist updated despite rollback: %+v", p)
	}
	if err := svc.Playlists.replaceChannels(ctx, 12345, "", "", feed()); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("replace on missing playlist err = %v", err)
	}
}

func TestFavoritesSurviveRefreshAndCredentialChange(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	pid := addEmptyPlaylist(t, svc, "p")
	if err := svc.Playlists.replaceChannels(ctx, pid, "", "", feed(sample...)); err != nil {
		t.Fatal(err)
	}
	ids, _ := svc.Channels.IDs(ctx, channel.Filter{PlaylistID: pid})
	if err := svc.Channels.SetFavorite(ctx, ids[0], true); err != nil { // BBC One HD
		t.Fatal(err)
	}
	if err := svc.Channels.SetFavorite(ctx, ids[0], true); err != nil { // idempotent
		t.Fatal(err)
	}

	// Refresh with new credentials in every URL and a regrouped channel.
	refreshed := make([]channel.Input, len(sample))
	copy(refreshed, sample)
	for i := range refreshed {
		refreshed[i].URL = fmt.Sprintf("http://x/newuser/newpass/%d", i)
	}
	refreshed[0].Group = "UK Entertainment"
	refreshed[0].Name = "  BBC  one hd " // whitespace/case changes are tolerated
	if err := svc.Playlists.replaceChannels(ctx, pid, "", "", feed(refreshed...)); err != nil {
		t.Fatal(err)
	}

	got := channelNames(t, svc, channel.Filter{PlaylistID: pid, FavoritesOnly: true})
	if !reflect.DeepEqual(got, []string{"  BBC  one hd "}) {
		t.Fatalf("favorites after refresh = %q (SD variant sharing tvg-id must not be included)", got)
	}
	ids, _ = svc.Channels.IDs(ctx, channel.Filter{PlaylistID: pid, FavoritesOnly: true})
	if ch, _ := svc.Channels.Get(ctx, ids[0]); !ch.Favorite {
		t.Error("Channel.Favorite not set")
	}
	if err := svc.Channels.SetFavorite(ctx, ids[0], false); err != nil {
		t.Fatal(err)
	}
	if got := channelNames(t, svc, channel.Filter{PlaylistID: pid, FavoritesOnly: true}); len(got) != 0 {
		t.Errorf("after unfavorite = %q", got)
	}
	if err := svc.Channels.SetFavorite(ctx, 999999, true); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("SetFavorite(missing) err = %v", err)
	}
}

func TestDeletePlaylistRemovesChannelsAndFavorites(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	a := addEmptyPlaylist(t, svc, "a")
	b := addEmptyPlaylist(t, svc, "b")
	svc.Playlists.replaceChannels(ctx, a, "", "", feed(channel.Input{Name: "BBC Other", URL: "http://e/o"}))
	svc.Playlists.replaceChannels(ctx, b, "", "", feed(sample...))
	ids, _ := svc.Channels.IDs(ctx, channel.Filter{PlaylistID: b})
	svc.Channels.SetFavorite(ctx, ids[0], true)

	if err := svc.Playlists.Delete(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Playlists.Get(ctx, b); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Get after delete err = %v", err)
	}
	if _, err := svc.Channels.Get(ctx, ids[0]); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("channel of deleted playlist still there: %v", err)
	}
	if got := channelNames(t, svc, channel.Filter{PlaylistID: a, Query: "bbc"}); !reflect.DeepEqual(got, []string{"BBC Other"}) {
		t.Errorf("search in remaining playlist = %q", got)
	}
	// SQLite reuses the highest rowid, so a new playlist gets b's ID. It must
	// not inherit b's favorites.
	c := addEmptyPlaylist(t, svc, "c")
	if c != b {
		t.Fatalf("new playlist ID %d, want reused %d", c, b)
	}
	svc.Playlists.replaceChannels(ctx, c, "", "", feed(sample...))
	if got := channelNames(t, svc, channel.Filter{PlaylistID: c, FavoritesOnly: true}); len(got) != 0 {
		t.Errorf("favorites survived delete: %q", got)
	}
	if err := svc.Playlists.Delete(ctx, 999999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Delete(missing) err = %v", err)
	}
}

func TestProgramGuide(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	now := time.Date(2026, 10, 20, 12, 30, 0, 0, time.UTC)
	svc.Playlists.Now = func() time.Time { return now }
	guide := atomic.Value{}
	guide.Store(`<tv><channel id="a"><display-name>Good One</display-name></channel>
<programme start="20261020120000 +0000" stop="20261020130000 +0000" channel="a"><title>On Now</title></programme></tv>`)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/epg.xml", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, guide.Load().(string)) })
	mux.HandleFunc("/other.xml", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/list.m3u", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `#EXTM3U x-tvg-url="ftp://nope/,`+srv.URL+`/epg.xml"`+"\n"+playlistBody[len("#EXTM3U\n"):])
	})

	p, _, err := svc.Playlists.Add(ctx, "P", srv.URL+"/list.m3u", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.TvgURL != srv.URL+"/epg.xml" || p.GuideSource() != p.TvgURL || !p.GuideRefreshedAt.IsZero() {
		t.Fatalf("playlist = %+v", p)
	}
	res, err := svc.Playlists.RefreshGuide(ctx, p.ID, nil)
	if err != nil || res.Programmes != 1 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	onNow := func() string {
		t.Helper()
		cs := allChannels(t, svc, p.ID)
		info, err := epg.NewRepository(svc.db).Current(ctx, []epg.Ref{{ChannelID: cs[0].ID, PlaylistID: p.ID, TvgID: cs[0].TvgID, Name: cs[0].Name}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if ps := info[cs[0].ID].Programmes; len(ps) == 1 {
			return ps[0].Title
		}
		return ""
	}
	if got := onNow(); got != "On Now" {
		t.Errorf("on now = %q", got)
	}
	if p, _ = svc.Playlists.Get(ctx, p.ID); !p.GuideRefreshedAt.Equal(now) {
		t.Errorf("guide refreshed at %v", p.GuideRefreshedAt)
	}
	// Only a change of the source in use makes the guide due again.
	if err := svc.Playlists.SetGuideURL(ctx, p.ID, p.TvgURL); err != nil {
		t.Fatal(err)
	}
	if p, _ = svc.Playlists.Get(ctx, p.ID); !p.GuideRefreshedAt.Equal(now) {
		t.Errorf("same source: guide refreshed at %v", p.GuideRefreshedAt)
	}

	// A failed import keeps the previous guide.
	guide.Store(`<html>maintenance</html>`)
	if _, err := svc.Playlists.RefreshGuide(ctx, p.ID, nil); !errors.Is(err, epg.ErrNotXMLTV) {
		t.Errorf("err = %v, want ErrNotXMLTV", err)
	}
	if err := svc.Playlists.SetGuideURL(ctx, p.ID, srv.URL+"/other.xml"); err != nil {
		t.Fatal(err)
	}
	if p, _ = svc.Playlists.Get(ctx, p.ID); !p.GuideRefreshedAt.IsZero() {
		t.Errorf("new source: guide refreshed at %v", p.GuideRefreshedAt)
	}
	if _, err := svc.Playlists.RefreshGuide(ctx, p.ID, nil); err == nil || strings.Contains(err.Error(), "other.xml") {
		t.Errorf("err = %v, want an error without the URL", err)
	}
	if got := onNow(); got != "On Now" {
		t.Errorf("guide lost after failed refresh: %q", got)
	}

	if err := svc.Playlists.SetGuideURL(ctx, p.ID, "file:///etc/passwd"); err == nil {
		t.Error("file URL accepted")
	}
	// Back to the playlist's URL: the guide is kept.
	if err := svc.Playlists.SetGuideURL(ctx, p.ID, " "); err != nil {
		t.Fatal(err)
	}
	if p, _ = svc.Playlists.Get(ctx, p.ID); p.GuideURL != "" || p.GuideSource() != p.TvgURL || onNow() != "On Now" {
		t.Errorf("after reset: %+v", p)
	}

	// A refresh whose header no longer names a guide drops the old one.
	cs := allChannels(t, svc, p.ID)
	if err := svc.Playlists.replaceChannels(ctx, p.ID, "", "", feed(channel.Input{Name: cs[0].Name, TvgID: cs[0].TvgID, URL: "http://example.com/1"})); err != nil {
		t.Fatal(err)
	}
	if got := onNow(); got != "" {
		t.Errorf("guide kept without a guide URL: %q", got)
	}
}

func TestGuideURLFromHeader(t *testing.T) {
	cases := []struct {
		header map[string]string
		want   string
	}{
		{map[string]string{}, ""},
		{map[string]string{"url-tvg": "http://a/e.xml"}, "http://a/e.xml"},
		{map[string]string{"x-tvg-url": " , javascript:x,https://b/e.xml.gz , http://c/"}, "https://b/e.xml.gz"},
		{map[string]string{"tvg-url": "file:///x"}, ""},
	}
	for _, c := range cases {
		if got := guideURLFromHeader(c.header); got != c.want {
			t.Errorf("guideURLFromHeader(%v) = %q, want %q", c.header, got, c.want)
		}
	}
}
