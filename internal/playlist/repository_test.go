package playlist

import (
	"context"
	"errors"
	"testing"
	"time"

	"iptv-client/internal/database"
)

func TestRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	r := newServices(t).Playlists.repo
	now := time.Now()
	id, err := r.Create(ctx, "B list", SourceURL, "http://example.com/b.m3u", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(ctx, "a list", SourceFile, `C:\a.m3u`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(ctx, "bad", "ftp", "x", now); err == nil {
		t.Error("accepted invalid source type")
	}
	ps, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Name != "a list" || ps[1].Name != "B list" {
		t.Fatalf("List = %+v", ps)
	}
	if !ps[1].LastRefreshedAt.IsZero() {
		t.Error("new playlist has LastRefreshedAt set")
	}
	if exists, err := r.Exists(ctx, id); err != nil || !exists {
		t.Errorf("Exists = %v, %v", exists, err)
	}
	if err := r.Rename(ctx, id, "Renamed"); err != nil {
		t.Fatal(err)
	}
	p, err := r.Get(ctx, id)
	if err != nil || p.Name != "Renamed" || p.SourceType != SourceURL {
		t.Fatalf("Get = %+v, %v", p, err)
	}
	if err := r.SaveRefresh(ctx, id, 5, "etag1", "lm1", "", now); err != nil {
		t.Fatal(err)
	}
	if p, _ := r.Get(ctx, id); p.ChannelCount != 5 || p.ETag != "etag1" || p.LastModified != "lm1" || p.LastRefreshedAt.IsZero() {
		t.Errorf("after SaveRefresh = %+v", p)
	}
	if err := r.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, id); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
	if exists, err := r.Exists(ctx, id); err != nil || exists {
		t.Errorf("Exists after delete = %v, %v", exists, err)
	}
	if err := r.Delete(ctx, id); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("double delete err = %v", err)
	}
	if err := r.MarkRefreshed(ctx, id, now); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("MarkRefreshed on missing playlist err = %v", err)
	}
}
