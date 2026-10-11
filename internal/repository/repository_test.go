package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"iptv-client/internal/model"
)

func openTest(t testing.TB) *Database {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "sub", "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// replaceChannels mirrors what the service layer does on a refresh.
func replaceChannels(t testing.TB, d *Database, playlistID int64, cs ...model.ChannelInput) {
	t.Helper()
	ctx := context.Background()
	err := d.InTx(ctx, func(tx Repositories) error {
		if err := tx.Channels.DeleteAllInPlaylist(ctx, playlistID); err != nil {
			return err
		}
		ins, err := tx.Channels.NewInserter(ctx, playlistID)
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, c := range cs {
			if err := ins.Insert(ctx, c); err != nil {
				return err
			}
		}
		if err := ins.Flush(ctx); err != nil {
			return err
		}
		if err := tx.Channels.IndexAllInPlaylist(ctx, playlistID); err != nil {
			return err
		}
		return tx.Playlists.SaveRefresh(ctx, playlistID, ins.Count(), "etag1", "lm1", time.Now())
	})
	if err != nil {
		t.Fatalf("replaceChannels: %v", err)
	}
}

func names(t *testing.T, d *Database, f model.ChannelFilter) []string {
	t.Helper()
	ctx := context.Background()
	ids, err := d.Channels.ListIDs(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := d.Channels.ListByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

var sample = []model.ChannelInput{
	{Name: "BBC One HD", URL: "http://x/user/pass/1", Group: "UK", TvgID: "bbc1.uk", Options: map[string]string{"http-referrer": "http://r/"}},
	{Name: "BBC One SD", URL: "http://x/user/pass/2", Group: "UK", TvgID: "bbc1.uk"},
	{Name: "CNN International", URL: "http://x/user/pass/3", Group: "News"},
	{Name: "Télé Café", URL: "http://x/user/pass/4", Group: "News"},
	{Name: "Loose", URL: "http://x/user/pass/5"},
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	for range 2 {
		d, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
}

func TestMigrateUpgradesLegacyVersionTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the version table as the pre-golang-migrate migrator left it.
	if _, err := d.conn.ExecContext(ctx, `
DROP TABLE schema_migrations;
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
INSERT INTO schema_migrations(version) VALUES (1), (2);`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d, err = Open(ctx, path) // would fail re-creating tables if versions were lost
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var version int
	var dirty bool
	if err := d.conn.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != 2 || dirty {
		t.Fatalf("got version %d dirty %v, want 2 false", version, dirty)
	}
}

func TestMigrationsDownAndUp(t *testing.T) {
	d := openTest(t)
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	drv, err := sqlite.WithInstance(d.conn, &sqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up after down: %v", err)
	}
}

func TestPlaylistCRUD(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	now := time.Now()
	id, err := d.Playlists.Create(ctx, "B list", model.SourceURL, "http://example.com/b.m3u", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Playlists.Create(ctx, "a list", model.SourceFile, `C:\a.m3u`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Playlists.Create(ctx, "bad", "ftp", "x", now); err == nil {
		t.Error("accepted invalid source type")
	}
	ps, err := d.Playlists.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Name != "a list" || ps[1].Name != "B list" {
		t.Fatalf("List = %+v", ps)
	}
	if !ps[1].LastRefreshedAt.IsZero() {
		t.Error("new playlist has LastRefreshedAt set")
	}
	if exists, err := d.Playlists.Exists(ctx, id); err != nil || !exists {
		t.Errorf("Exists = %v, %v", exists, err)
	}
	if err := d.Playlists.Rename(ctx, id, "Renamed"); err != nil {
		t.Fatal(err)
	}
	p, err := d.Playlists.Get(ctx, id)
	if err != nil || p.Name != "Renamed" || p.SourceType != model.SourceURL {
		t.Fatalf("Get = %+v, %v", p, err)
	}
	if err := d.Playlists.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Playlists.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
	if exists, err := d.Playlists.Exists(ctx, id); err != nil || exists {
		t.Errorf("Exists after delete = %v, %v", exists, err)
	}
	if err := d.Playlists.Delete(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete err = %v", err)
	}
	if err := d.Playlists.MarkRefreshed(ctx, id, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkRefreshed on missing playlist err = %v", err)
	}
}

func TestChannelsGroupsSearch(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	pid, _ := d.Playlists.Create(ctx, "p", model.SourceURL, "http://e/p.m3u", time.Now())
	replaceChannels(t, d, pid, sample...)
	p, _ := d.Playlists.Get(ctx, pid)
	if p.ChannelCount != 5 || p.ETag != "etag1" || p.LastModified != "lm1" || p.LastRefreshedAt.IsZero() {
		t.Errorf("playlist after replace = %+v", p)
	}

	groups, err := d.Channels.ListGroups(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	wantGroups := []model.Group{{Name: "UK", Count: 2}, {Name: "News", Count: 2}, {Name: "", Count: 1}}
	if !reflect.DeepEqual(groups, wantGroups) {
		t.Errorf("ListGroups = %+v, want %+v", groups, wantGroups)
	}

	// Favorite CNN so every filter combination (each a separate statement)
	// has something to find.
	ids, _ := d.Channels.ListIDs(ctx, model.ChannelFilter{PlaylistID: pid})
	favPID, favKey, err := d.Channels.FavoriteKey(ctx, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Favorites.Add(ctx, favPID, favKey, time.Now()); err != nil {
		t.Fatal(err)
	}

	news, uk, empty := "News", "UK", ""
	cases := []struct {
		f    model.ChannelFilter
		want []string
	}{
		{model.ChannelFilter{PlaylistID: pid}, []string{"BBC One HD", "BBC One SD", "CNN International", "Télé Café", "Loose"}},
		{model.ChannelFilter{PlaylistID: pid, Group: &news}, []string{"CNN International", "Télé Café"}},
		{model.ChannelFilter{PlaylistID: pid, Group: &empty}, []string{"Loose"}},
		{model.ChannelFilter{PlaylistID: pid, FavoritesOnly: true}, []string{"CNN International"}},
		{model.ChannelFilter{PlaylistID: pid, Group: &news, FavoritesOnly: true}, []string{"CNN International"}},
		{model.ChannelFilter{PlaylistID: pid, Group: &uk, FavoritesOnly: true}, []string{}},
		{model.ChannelFilter{PlaylistID: pid, Query: "bbc"}, []string{"BBC One HD", "BBC One SD"}},
		{model.ChannelFilter{PlaylistID: pid, Query: "bbc s"}, []string{"BBC One SD"}},
		{model.ChannelFilter{PlaylistID: pid, Query: "tele cafe"}, []string{"Télé Café"}}, // diacritics folded
		{model.ChannelFilter{PlaylistID: pid, Query: "news"}, []string{"CNN International", "Télé Café"}},
		{model.ChannelFilter{PlaylistID: pid, Query: `" OR * NEAR( -`}, []string{}}, // FTS syntax is literal
		{model.ChannelFilter{PlaylistID: pid, Query: "c", Group: &news}, []string{"CNN International", "Télé Café"}},
		{model.ChannelFilter{PlaylistID: pid, Query: "c", Group: &uk}, []string{}},
		{model.ChannelFilter{PlaylistID: pid, Query: "news", FavoritesOnly: true}, []string{"CNN International"}},
		{model.ChannelFilter{PlaylistID: pid, Query: "c", Group: &news, FavoritesOnly: true}, []string{"CNN International"}},
		{model.ChannelFilter{PlaylistID: pid + 1}, []string{}},
	}
	for _, c := range cases {
		if got := names(t, d, c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("filter %+v = %q, want %q", c.f, got, c.want)
		}
	}

	ch, err := d.Channels.Get(ctx, ids[0])
	if err != nil || ch.Options["http-referrer"] != "http://r/" || ch.TvgID != "bbc1.uk" || ch.URL != "http://x/user/pass/1" {
		t.Errorf("Get = %+v, %v", ch, err)
	}
	if ch, _ := d.Channels.Get(ctx, ids[2]); !ch.Favorite {
		t.Error("Get: Favorite not set")
	}
	// ListByIDs preserves the requested order and skips unknown IDs.
	cs, _ := d.Channels.ListByIDs(ctx, []int64{ids[2], 999999, ids[0]})
	if len(cs) != 2 || cs[0].ID != ids[2] || cs[1].ID != ids[0] || !cs[0].Favorite || cs[1].Favorite {
		t.Errorf("ListByIDs = %+v", cs)
	}
	if _, err := d.Channels.Get(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) err = %v", err)
	}
	if _, _, err := d.Channels.FavoriteKey(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("FavoriteKey(missing) err = %v", err)
	}
}

func TestInTxRollsBack(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	boom := errors.New("boom")
	err := d.InTx(ctx, func(tx Repositories) error {
		if _, err := tx.Playlists.Create(ctx, "p", model.SourceURL, "http://e/p", time.Now()); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if ps, _ := d.Playlists.List(ctx); len(ps) != 0 {
		t.Errorf("rolled-back playlist kept: %+v", ps)
	}
}

func TestFavorites(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	pid, _ := d.Playlists.Create(ctx, "p", model.SourceURL, "http://e/p.m3u", time.Now())
	replaceChannels(t, d, pid, sample...)
	ids, _ := d.Channels.ListIDs(ctx, model.ChannelFilter{PlaylistID: pid})
	_, key, err := d.Channels.FavoriteKey(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // Add is idempotent
		if err := d.Favorites.Add(ctx, pid, key, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := names(t, d, model.ChannelFilter{PlaylistID: pid, FavoritesOnly: true}); !reflect.DeepEqual(got, []string{"BBC One HD"}) {
		t.Errorf("favorites = %q", got)
	}
	for range 2 { // Remove is idempotent
		if err := d.Favorites.Remove(ctx, pid, key); err != nil {
			t.Fatal(err)
		}
	}
	if got := names(t, d, model.ChannelFilter{PlaylistID: pid, FavoritesOnly: true}); len(got) != 0 {
		t.Errorf("after remove = %q", got)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	if v, err := d.Settings.Get(ctx, "k"); v != "" || err != nil {
		t.Fatalf("unset = %q, %v", v, err)
	}
	d.Settings.Set(ctx, "k", "1")
	d.Settings.Set(ctx, "k", "2")
	if v, _ := d.Settings.Get(ctx, "k"); v != "2" {
		t.Errorf("got %q, want 2", v)
	}
	d.Settings.Delete(ctx, "k")
	if v, _ := d.Settings.Get(ctx, "k"); v != "" {
		t.Errorf("after delete got %q", v)
	}
}

// TestBulkInsert200k checks the critique's performance target: a 200k channel
// playlist must import in well under the 5s budget.
func TestBulkInsert200k(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	ctx := context.Background()
	d := openTest(t)
	pid, _ := d.Playlists.Create(ctx, "big", model.SourceURL, "http://e/big", time.Now())
	const total = 200_000
	big := make([]model.ChannelInput, total)
	for i := range total {
		big[i] = model.ChannelInput{
			Name:  fmt.Sprintf("Channel %d HD", i),
			URL:   fmt.Sprintf("http://stream.example.com/live/user/pass/%d.ts", i),
			Group: fmt.Sprintf("Group %d", i%300),
			TvgID: fmt.Sprintf("ch%d.tv", i),
		}
	}
	start := time.Now()
	replaceChannels(t, d, pid, big...)
	insertDur := time.Since(start)

	start = time.Now()
	ids, err := d.Channels.ListIDs(ctx, model.ChannelFilter{PlaylistID: pid})
	idsDur := time.Since(start)
	if err != nil || len(ids) != total {
		t.Fatalf("ListIDs = %d, %v", len(ids), err)
	}
	if c, _ := d.Channels.Get(ctx, ids[total-1]); c.Name != "Channel 199999 HD" {
		t.Errorf("last channel = %+v", c)
	}
	start = time.Now()
	hits, err := d.Channels.ListIDs(ctx, model.ChannelFilter{PlaylistID: pid, Query: "channel 1999"})
	searchDur := time.Since(start)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search = %d, %v", len(hits), err)
	}
	start = time.Now()
	if _, err := d.Channels.ListGroups(ctx, pid); err != nil {
		t.Fatal(err)
	}
	groupsDur := time.Since(start)

	// A refresh replaces existing rows, so it also pays for deleting them.
	start = time.Now()
	replaceChannels(t, d, pid, big...)
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

func TestDeleteAllInPlaylistKeepsIndexConsistent(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	a, _ := d.Playlists.Create(ctx, "a", model.SourceURL, "http://e/a", time.Now())
	b, _ := d.Playlists.Create(ctx, "b", model.SourceURL, "http://e/b", time.Now())
	replaceChannels(t, d, a, sample...)
	replaceChannels(t, d, b, model.ChannelInput{Name: "BBC Other", URL: "http://e/o"})
	ids, _ := d.Channels.ListIDs(ctx, model.ChannelFilter{PlaylistID: a})
	_, key, _ := d.Channels.FavoriteKey(ctx, ids[0])
	d.Favorites.Add(ctx, a, key, time.Now())

	err := d.InTx(ctx, func(tx Repositories) error {
		if err := tx.Channels.DeleteAllInPlaylist(ctx, a); err != nil {
			return err
		}
		return tx.Playlists.Delete(ctx, a)
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	d.conn.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&n)
	if n != 1 {
		t.Errorf("channels left = %d, want 1", n)
	}
	d.conn.QueryRow(`SELECT COUNT(*) FROM favorites`).Scan(&n)
	if n != 0 {
		t.Errorf("favorites left = %d, want 0", n)
	}
	// The FTS index must not keep entries for deleted channels.
	if _, err := d.conn.Exec(`INSERT INTO channels_fts(channels_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Errorf("FTS integrity check failed: %v", err)
	}
	if got := names(t, d, model.ChannelFilter{PlaylistID: b, Query: "bbc"}); !reflect.DeepEqual(got, []string{"BBC Other"}) {
		t.Errorf("search in remaining playlist = %q", got)
	}
}
