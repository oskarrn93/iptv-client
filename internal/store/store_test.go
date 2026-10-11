package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func openTest(t testing.TB) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sub", "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func feed(cs ...ChannelInput) func(func(ChannelInput) error) error {
	return func(insert func(ChannelInput) error) error {
		for _, c := range cs {
			if err := insert(c); err != nil {
				return err
			}
		}
		return nil
	}
}

func names(t *testing.T, s *Store, f ChannelFilter) []string {
	t.Helper()
	ctx := context.Background()
	ids, err := s.ChannelIDs(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := s.ChannelsByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

var sample = []ChannelInput{
	{Name: "BBC One HD", URL: "http://x/user/pass/1", Group: "UK", TvgID: "bbc1.uk", Options: map[string]string{"http-referrer": "http://r/"}},
	{Name: "BBC One SD", URL: "http://x/user/pass/2", Group: "UK", TvgID: "bbc1.uk"},
	{Name: "CNN International", URL: "http://x/user/pass/3", Group: "News"},
	{Name: "Télé Café", URL: "http://x/user/pass/4", Group: "News"},
	{Name: "Loose", URL: "http://x/user/pass/5"},
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	for range 2 {
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestPlaylistCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id, err := s.AddPlaylist(ctx, "B list", SourceURL, "http://example.com/b.m3u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlaylist(ctx, "a list", SourceFile, `C:\a.m3u`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlaylist(ctx, "bad", "ftp", "x"); err == nil {
		t.Error("accepted invalid source type")
	}
	ps, err := s.ListPlaylists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Name != "a list" || ps[1].Name != "B list" {
		t.Fatalf("ListPlaylists = %+v", ps)
	}
	if !ps[1].LastRefreshedAt.IsZero() {
		t.Error("new playlist has LastRefreshedAt set")
	}
	if err := s.RenamePlaylist(ctx, id, "Renamed"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPlaylist(ctx, id)
	if err != nil || p.Name != "Renamed" || p.SourceType != SourceURL {
		t.Fatalf("GetPlaylist = %+v, %v", p, err)
	}
	if err := s.DeletePlaylist(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPlaylist(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
	if err := s.DeletePlaylist(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete err = %v", err)
	}
}

func TestChannelsGroupsSearch(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	pid, _ := s.AddPlaylist(ctx, "p", SourceURL, "http://e/p.m3u")
	err := s.ReplaceChannels(ctx, pid, "etag1", "lm1", feed(sample...))
	if err != nil {
		t.Fatalf("ReplaceChannels: %v", err)
	}
	p, _ := s.GetPlaylist(ctx, pid)
	if p.ChannelCount != 5 || p.ETag != "etag1" || p.LastModified != "lm1" || p.LastRefreshedAt.IsZero() {
		t.Errorf("playlist after replace = %+v", p)
	}

	groups, err := s.ListGroups(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	wantGroups := []Group{{"UK", 2}, {"News", 2}, {"", 1}}
	if !reflect.DeepEqual(groups, wantGroups) {
		t.Errorf("ListGroups = %+v, want %+v", groups, wantGroups)
	}

	news, empty := "News", ""
	cases := []struct {
		f    ChannelFilter
		want []string
	}{
		{ChannelFilter{PlaylistID: pid}, []string{"BBC One HD", "BBC One SD", "CNN International", "Télé Café", "Loose"}},
		{ChannelFilter{PlaylistID: pid, Group: &news}, []string{"CNN International", "Télé Café"}},
		{ChannelFilter{PlaylistID: pid, Group: &empty}, []string{"Loose"}},
		{ChannelFilter{PlaylistID: pid, Query: "bbc"}, []string{"BBC One HD", "BBC One SD"}},
		{ChannelFilter{PlaylistID: pid, Query: "bbc s"}, []string{"BBC One SD"}},
		{ChannelFilter{PlaylistID: pid, Query: "tele cafe"}, []string{"Télé Café"}}, // diacritics folded
		{ChannelFilter{PlaylistID: pid, Query: "news"}, []string{"CNN International", "Télé Café"}},
		{ChannelFilter{PlaylistID: pid, Query: `" OR * NEAR( -`}, []string{}}, // FTS syntax is literal
		{ChannelFilter{PlaylistID: pid + 1}, []string{}},
	}
	for _, c := range cases {
		if got := names(t, s, c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("filter %+v = %q, want %q", c.f, got, c.want)
		}
	}

	ids, _ := s.ChannelIDs(ctx, ChannelFilter{PlaylistID: pid})
	ch, err := s.GetChannel(ctx, ids[0])
	if err != nil || ch.Options["http-referrer"] != "http://r/" || ch.TvgID != "bbc1.uk" {
		t.Errorf("GetChannel = %+v, %v", ch, err)
	}
	// ChannelsByIDs preserves the requested order and skips unknown IDs.
	cs, _ := s.ChannelsByIDs(ctx, []int64{ids[2], 999999, ids[0]})
	if len(cs) != 2 || cs[0].ID != ids[2] || cs[1].ID != ids[0] {
		t.Errorf("ChannelsByIDs order = %+v", cs)
	}
	if _, err := s.GetChannel(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetChannel(missing) err = %v", err)
	}
}

func TestReplaceRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	pid, _ := s.AddPlaylist(ctx, "p", SourceURL, "http://e/p.m3u")
	if err := s.ReplaceChannels(ctx, pid, "", "", feed(sample...)); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("parse failed")
	err := s.ReplaceChannels(ctx, pid, "new", "", func(insert func(ChannelInput) error) error {
		insert(ChannelInput{Name: "Half", URL: "http://x/h"})
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got := names(t, s, ChannelFilter{PlaylistID: pid}); len(got) != 5 {
		t.Errorf("after failed replace got %q, want original 5", got)
	}
	if got := names(t, s, ChannelFilter{PlaylistID: pid, Query: "half"}); len(got) != 0 {
		t.Errorf("FTS has rolled-back row: %q", got)
	}
	if p, _ := s.GetPlaylist(ctx, pid); p.ETag == "new" {
		t.Error("etag updated despite rollback")
	}
	if err := s.ReplaceChannels(ctx, 12345, "", "", feed()); !errors.Is(err, ErrNotFound) {
		t.Errorf("replace on missing playlist err = %v", err)
	}
}

func TestFavoritesSurviveRefreshAndCredentialChange(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	pid, _ := s.AddPlaylist(ctx, "p", SourceURL, "http://e/p.m3u")
	s.ReplaceChannels(ctx, pid, "", "", feed(sample...))
	ids, _ := s.ChannelIDs(ctx, ChannelFilter{PlaylistID: pid})
	if err := s.SetFavorite(ctx, ids[0], true); err != nil { // BBC One HD
		t.Fatal(err)
	}
	if err := s.SetFavorite(ctx, ids[0], true); err != nil { // idempotent
		t.Fatal(err)
	}

	// Refresh with new credentials in every URL and a regrouped channel.
	refreshed := make([]ChannelInput, len(sample))
	copy(refreshed, sample)
	for i := range refreshed {
		refreshed[i].URL = fmt.Sprintf("http://x/newuser/newpass/%d", i)
	}
	refreshed[0].Group = "UK Entertainment"
	refreshed[0].Name = "  BBC  one hd " // whitespace/case changes are tolerated
	if err := s.ReplaceChannels(ctx, pid, "", "", feed(refreshed...)); err != nil {
		t.Fatal(err)
	}

	got := names(t, s, ChannelFilter{PlaylistID: pid, FavoritesOnly: true})
	if !reflect.DeepEqual(got, []string{"  BBC  one hd "}) {
		t.Fatalf("favorites after refresh = %q (SD variant sharing tvg-id must not be included)", got)
	}
	ids, _ = s.ChannelIDs(ctx, ChannelFilter{PlaylistID: pid, FavoritesOnly: true})
	if ch, _ := s.GetChannel(ctx, ids[0]); !ch.Favorite {
		t.Error("Channel.Favorite not set")
	}
	if err := s.SetFavorite(ctx, ids[0], false); err != nil {
		t.Fatal(err)
	}
	if got := names(t, s, ChannelFilter{PlaylistID: pid, FavoritesOnly: true}); len(got) != 0 {
		t.Errorf("after unfavorite = %q", got)
	}
}

func TestDeletePlaylistCascades(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	a, _ := s.AddPlaylist(ctx, "a", SourceURL, "http://e/a")
	b, _ := s.AddPlaylist(ctx, "b", SourceURL, "http://e/b")
	s.ReplaceChannels(ctx, a, "", "", feed(sample...))
	s.ReplaceChannels(ctx, b, "", "", feed(ChannelInput{Name: "BBC Other", URL: "http://e/o"}))
	ids, _ := s.ChannelIDs(ctx, ChannelFilter{PlaylistID: a})
	s.SetFavorite(ctx, ids[0], true)

	if err := s.DeletePlaylist(ctx, a); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&n)
	if n != 1 {
		t.Errorf("channels left = %d, want 1", n)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM favorites`).Scan(&n)
	if n != 0 {
		t.Errorf("favorites left = %d, want 0", n)
	}
	// The FTS index must not keep entries for cascaded deletes.
	if _, err := s.db.Exec(`INSERT INTO channels_fts(channels_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Errorf("FTS integrity check failed: %v", err)
	}
	if got := names(t, s, ChannelFilter{PlaylistID: b, Query: "bbc"}); !reflect.DeepEqual(got, []string{"BBC Other"}) {
		t.Errorf("search in remaining playlist = %q", got)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if v, err := s.GetSetting(ctx, "k"); v != "" || err != nil {
		t.Fatalf("unset = %q, %v", v, err)
	}
	s.SetSetting(ctx, "k", "1")
	s.SetSetting(ctx, "k", "2")
	if v, _ := s.GetSetting(ctx, "k"); v != "2" {
		t.Errorf("got %q, want 2", v)
	}
	s.SetSetting(ctx, "k", "")
	if v, _ := s.GetSetting(ctx, "k"); v != "" {
		t.Errorf("after clear got %q", v)
	}
}

// TestBulkInsert200k checks the critique's performance target: a 200k channel
// playlist must import in well under the 5s budget.
func TestBulkInsert200k(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	ctx := context.Background()
	s := openTest(t)
	pid, _ := s.AddPlaylist(ctx, "big", SourceURL, "http://e/big")
	const total = 200_000
	gen := func(insert func(ChannelInput) error) error {
		for i := range total {
			err := insert(ChannelInput{
				Name:  fmt.Sprintf("Channel %d HD", i),
				URL:   fmt.Sprintf("http://stream.example.com/live/user/pass/%d.ts", i),
				Group: fmt.Sprintf("Group %d", i%300),
				TvgID: fmt.Sprintf("ch%d.tv", i),
			})
			if err != nil {
				return err
			}
		}
		return nil
	}
	start := time.Now()
	err := s.ReplaceChannels(ctx, pid, "", "", gen)
	insertDur := time.Since(start)
	if err != nil {
		t.Fatalf("ReplaceChannels: %v", err)
	}

	start = time.Now()
	ids, err := s.ChannelIDs(ctx, ChannelFilter{PlaylistID: pid})
	idsDur := time.Since(start)
	if err != nil || len(ids) != total {
		t.Fatalf("ChannelIDs = %d, %v", len(ids), err)
	}
	start = time.Now()
	hits, err := s.ChannelIDs(ctx, ChannelFilter{PlaylistID: pid, Query: "channel 1999"})
	searchDur := time.Since(start)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search = %d, %v", len(hits), err)
	}
	start = time.Now()
	if _, err := s.ListGroups(ctx, pid); err != nil {
		t.Fatal(err)
	}
	groupsDur := time.Since(start)

	// A refresh replaces existing rows, so it also pays for deleting them.
	start = time.Now()
	if err := s.ReplaceChannels(ctx, pid, "", "", gen); err != nil {
		t.Fatal(err)
	}
	refreshDur := time.Since(start)

	t.Logf("insert %d: %v | refresh: %v | all ids: %v | search: %v (%d hits) | groups: %v",
		total, insertDur, refreshDur, idsDur, searchDur, len(hits), groupsDur)
	if insertDur > 5*time.Second {
		t.Errorf("insert took %v, budget 5s", insertDur)
	}
	if refreshDur > 8*time.Second {
		t.Errorf("refresh took %v, budget 8s", refreshDur)
	}
	if searchDur > 300*time.Millisecond {
		t.Errorf("search took %v, budget 300ms", searchDur)
	}
}
