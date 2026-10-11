package setting

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"iptv-client/internal/database"
)

func newService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db)
}

func TestRepository(t *testing.T) {
	ctx := context.Background()
	r := newService(t).repo
	if v, err := r.Get(ctx, "k"); v != "" || err != nil {
		t.Fatalf("unset = %q, %v", v, err)
	}
	r.Set(ctx, "k", "1")
	r.Set(ctx, "k", "2")
	if v, _ := r.Get(ctx, "k"); v != "2" {
		t.Errorf("got %q, want 2", v)
	}
	r.Delete(ctx, "k")
	if v, _ := r.Get(ctx, "k"); v != "" {
		t.Errorf("after delete got %q", v)
	}
}

func TestSetUserAgent(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	if err := svc.SetUserAgent(ctx, " Custom/2 "); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.UserAgent(ctx); err != nil || got != "Custom/2" {
		t.Errorf("UserAgent = %q, %v", got, err)
	}
	if err := svc.SetUserAgent(ctx, "a\r\nX-Injected: 1"); err == nil {
		t.Error("header injection accepted")
	}
	if err := svc.SetUserAgent(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.UserAgent(ctx); got != "" {
		t.Errorf("UserAgent after reset = %q", got)
	}
}

func TestSetVLCPath(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	dir := t.TempDir()
	evil := filepath.Join(dir, "cmd.exe")
	os.WriteFile(evil, nil, 0o755)
	if err := svc.SetVLCPath(ctx, evil); err == nil {
		t.Error("non-VLC path accepted")
	}
	vlc := filepath.Join(dir, "vlc.exe")
	os.WriteFile(vlc, nil, 0o755)
	if err := svc.SetVLCPath(ctx, vlc); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.VLCPath(ctx); err != nil || got != vlc {
		t.Errorf("VLCPath = %q, %v", got, err)
	}
	if err := svc.SetVLCPath(ctx, ""); err != nil {
		t.Fatal(err)
	}
}
