package channel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"iptv-client/internal/database"
)

func openTest(t testing.TB) *database.DB {
	t.Helper()
	d, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// createPlaylist inserts the playlist row channels belong to. The playlist
// package owns that table, but importing it here would be an import cycle.
func createPlaylist(t testing.TB, d *database.DB, name string) int64 {
	t.Helper()
	res, err := d.Exec(`
		INSERT INTO playlists (name, source_type, source, created_at)
		VALUES (?, 'url', ?, ?)`, name, "http://e/"+name, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// replaceChannels mirrors what the playlist package does on a refresh.
func replaceChannels(t testing.TB, d *database.DB, playlistID int64, cs ...Input) {
	t.Helper()
	ctx := context.Background()
	err := d.InTx(ctx, func(tx database.Querier) error {
		r := NewRepository(tx)
		if err := r.DeleteAllInPlaylist(ctx, playlistID); err != nil {
			return err
		}
		ins, err := r.NewInserter(ctx, playlistID)
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
		return r.IndexAllInPlaylist(ctx, playlistID)
	})
	if err != nil {
		t.Fatalf("replaceChannels: %v", err)
	}
}

func names(t *testing.T, r *Repository, f Filter) []string {
	t.Helper()
	ctx := context.Background()
	ids, err := r.ListIDs(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := r.ListByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

var sample = []Input{
	{Name: "BBC One HD", URL: "http://x/user/pass/1", Group: "UK", TvgID: "bbc1.uk", Options: map[string]string{"http-referrer": "http://r/"}},
	{Name: "BBC One SD", URL: "http://x/user/pass/2", Group: "UK", TvgID: "bbc1.uk"},
	{Name: "CNN International", URL: "http://x/user/pass/3", Group: "News"},
	{Name: "Télé Café", URL: "http://x/user/pass/4", Group: "News"},
	{Name: "Loose", URL: "http://x/user/pass/5"},
}

func TestChannelsGroupsSearch(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	r, favs := NewRepository(d), &favoriteRepository{q: d}
	pid := createPlaylist(t, d, "p")
	replaceChannels(t, d, pid, sample...)

	groups, err := r.ListGroups(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	wantGroups := []Group{{Name: "UK", Count: 2}, {Name: "News", Count: 2}, {Name: "", Count: 1}}
	if !reflect.DeepEqual(groups, wantGroups) {
		t.Errorf("ListGroups = %+v, want %+v", groups, wantGroups)
	}

	// Favorite CNN so every filter combination (each a separate statement)
	// has something to find.
	ids, _ := r.ListIDs(ctx, Filter{PlaylistID: pid})
	favPID, favKey, err := r.FavoriteKey(ctx, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := favs.Add(ctx, favPID, favKey, time.Now()); err != nil {
		t.Fatal(err)
	}

	news, uk, empty := "News", "UK", ""
	cases := []struct {
		f    Filter
		want []string
	}{
		{Filter{PlaylistID: pid}, []string{"BBC One HD", "BBC One SD", "CNN International", "Télé Café", "Loose"}},
		{Filter{PlaylistID: pid, Group: &news}, []string{"CNN International", "Télé Café"}},
		{Filter{PlaylistID: pid, Group: &empty}, []string{"Loose"}},
		{Filter{PlaylistID: pid, FavoritesOnly: true}, []string{"CNN International"}},
		{Filter{PlaylistID: pid, Group: &news, FavoritesOnly: true}, []string{"CNN International"}},
		{Filter{PlaylistID: pid, Group: &uk, FavoritesOnly: true}, []string{}},
		{Filter{PlaylistID: pid, Query: "bbc"}, []string{"BBC One HD", "BBC One SD"}},
		{Filter{PlaylistID: pid, Query: "bbc s"}, []string{"BBC One SD"}},
		{Filter{PlaylistID: pid, Query: "tele cafe"}, []string{"Télé Café"}}, // diacritics folded
		{Filter{PlaylistID: pid, Query: "news"}, []string{"CNN International", "Télé Café"}},
		{Filter{PlaylistID: pid, Query: `" OR * NEAR( -`}, []string{}}, // FTS syntax is literal
		{Filter{PlaylistID: pid, Query: "c", Group: &news}, []string{"CNN International", "Télé Café"}},
		{Filter{PlaylistID: pid, Query: "c", Group: &uk}, []string{}},
		{Filter{PlaylistID: pid, Query: "news", FavoritesOnly: true}, []string{"CNN International"}},
		{Filter{PlaylistID: pid, Query: "c", Group: &news, FavoritesOnly: true}, []string{"CNN International"}},
		{Filter{PlaylistID: pid + 1}, []string{}},
	}
	for _, c := range cases {
		if got := names(t, r, c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("filter %+v = %q, want %q", c.f, got, c.want)
		}
	}

	ch, err := r.Get(ctx, ids[0])
	if err != nil || ch.Options["http-referrer"] != "http://r/" || ch.TvgID != "bbc1.uk" || ch.URL != "http://x/user/pass/1" {
		t.Errorf("Get = %+v, %v", ch, err)
	}
	if ch, _ := r.Get(ctx, ids[2]); !ch.Favorite {
		t.Error("Get: Favorite not set")
	}
	// ListByIDs preserves the requested order and skips unknown IDs.
	cs, _ := r.ListByIDs(ctx, []int64{ids[2], 999999, ids[0]})
	if len(cs) != 2 || cs[0].ID != ids[2] || cs[1].ID != ids[0] || !cs[0].Favorite || cs[1].Favorite {
		t.Errorf("ListByIDs = %+v", cs)
	}
	if _, err := r.Get(ctx, 999999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Get(missing) err = %v", err)
	}
	if _, _, err := r.FavoriteKey(ctx, 999999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("FavoriteKey(missing) err = %v", err)
	}
}

func TestFavorites(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	r, favs := NewRepository(d), &favoriteRepository{q: d}
	pid := createPlaylist(t, d, "p")
	replaceChannels(t, d, pid, sample...)
	ids, _ := r.ListIDs(ctx, Filter{PlaylistID: pid})
	_, key, err := r.FavoriteKey(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // Add is idempotent
		if err := favs.Add(ctx, pid, key, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := names(t, r, Filter{PlaylistID: pid, FavoritesOnly: true}); !reflect.DeepEqual(got, []string{"BBC One HD"}) {
		t.Errorf("favorites = %q", got)
	}
	for range 2 { // Remove is idempotent
		if err := favs.Remove(ctx, pid, key); err != nil {
			t.Fatal(err)
		}
	}
	if got := names(t, r, Filter{PlaylistID: pid, FavoritesOnly: true}); len(got) != 0 {
		t.Errorf("after remove = %q", got)
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
	r := NewRepository(d)
	pid := createPlaylist(t, d, "big")
	const total = 200_000
	big := make([]Input, total)
	for i := range total {
		big[i] = Input{
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
	ids, err := r.ListIDs(ctx, Filter{PlaylistID: pid})
	idsDur := time.Since(start)
	if err != nil || len(ids) != total {
		t.Fatalf("ListIDs = %d, %v", len(ids), err)
	}
	if c, _ := r.Get(ctx, ids[total-1]); c.Name != "Channel 199999 HD" {
		t.Errorf("last channel = %+v", c)
	}
	start = time.Now()
	hits, err := r.ListIDs(ctx, Filter{PlaylistID: pid, Query: "channel 1999"})
	searchDur := time.Since(start)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search = %d, %v", len(hits), err)
	}
	start = time.Now()
	if _, err := r.ListGroups(ctx, pid); err != nil {
		t.Fatal(err)
	}
	groupsDur := time.Since(start)

	// A refresh replaces existing rows, so it also pays for deleting them.
	start = time.Now()
	replaceChannels(t, d, pid, big...)
	refreshDur := time.Since(start)

	t.Logf("insert %d: %v | refresh: %v | all ids: %v | search: %v (%d hits) | groups: %v",
		total, insertDur, refreshDur, idsDur, searchDur, len(hits), groupsDur)
	// Shared CI runners (and -race) are too noisy for wall-clock budgets.
	if os.Getenv("CI") != "" {
		return
	}
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
	r, favs := NewRepository(d), &favoriteRepository{q: d}
	a := createPlaylist(t, d, "a")
	b := createPlaylist(t, d, "b")
	replaceChannels(t, d, a, sample...)
	replaceChannels(t, d, b, Input{Name: "BBC Other", URL: "http://e/o"})
	ids, _ := r.ListIDs(ctx, Filter{PlaylistID: a})
	_, key, _ := r.FavoriteKey(ctx, ids[0])
	favs.Add(ctx, a, key, time.Now())

	err := d.InTx(ctx, func(tx database.Querier) error {
		if err := NewRepository(tx).DeleteAllInPlaylist(ctx, a); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM playlists WHERE id = ?`, a)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&n)
	if n != 1 {
		t.Errorf("channels left = %d, want 1", n)
	}
	d.QueryRow(`SELECT COUNT(*) FROM favorites`).Scan(&n)
	if n != 0 {
		t.Errorf("favorites left = %d, want 0", n)
	}
	// The FTS index must not keep entries for deleted channels.
	if _, err := d.Exec(`INSERT INTO channels_fts(channels_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Errorf("FTS integrity check failed: %v", err)
	}
	if got := names(t, r, Filter{PlaylistID: b, Query: "bbc"}); !reflect.DeepEqual(got, []string{"BBC Other"}) {
		t.Errorf("search in remaining playlist = %q", got)
	}
}
