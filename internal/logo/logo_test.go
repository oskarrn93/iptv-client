package logo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// png is the 8-byte PNG signature plus enough for content sniffing.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func newTest(t *testing.T) (*Cache, *httptest.Server, *atomic.Int32, *time.Time) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/logo.png":
			w.Write(png)
		case "/logo.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	now := time.Now()
	c := New(t.TempDir())
	c.Now = func() time.Time { return now }
	return c, srv, &hits, &now
}

func TestGetCachesLogos(t *testing.T) {
	ctx := context.Background()
	c, srv, hits, now := newTest(t)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			img, err := c.Get(ctx, srv.URL+"/logo.png", "ua")
			if err != nil || img.Type != "image/png" {
				t.Errorf("img = %v, err = %v", img.Type, err)
			}
		})
	}
	wg.Wait()
	if _, err := c.Get(ctx, srv.URL+"/logo.png", "ua"); err != nil || hits.Load() != 1 {
		t.Errorf("hits = %d, err = %v; want one shared download", hits.Load(), err)
	}
	*now = now.Add(fresh + time.Hour)
	if _, err := c.Get(ctx, srv.URL+"/logo.png", "ua"); err != nil || hits.Load() != 2 {
		t.Errorf("stale logo: hits = %d, err = %v", hits.Load(), err)
	}
}

func TestGetRejectsNonImages(t *testing.T) {
	ctx := context.Background()
	c, srv, hits, now := newTest(t)
	for _, path := range []string{"/logo.svg", "/missing"} {
		for range 2 {
			if _, err := c.Get(ctx, srv.URL+path, ""); !errors.Is(err, ErrNoLogo) {
				t.Errorf("%s: err = %v", path, err)
			}
		}
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d; failures should be cached", hits.Load())
	}
	*now = now.Add(failedFor + time.Minute)
	c.Get(ctx, srv.URL+"/missing", "")
	if hits.Load() != 3 {
		t.Errorf("hits = %d; failure should be retried later", hits.Load())
	}
	for _, u := range []string{"", "file:///etc/passwd", "data:image/png;base64,AAAA", "javascript:x", "http://"} {
		if _, err := c.Get(ctx, u, ""); !errors.Is(err, ErrNoLogo) {
			t.Errorf("%q: err = %v", u, err)
		}
	}
}

func TestStaleLogoKeptWhenRefreshFails(t *testing.T) {
	ctx := context.Background()
	c, srv, _, now := newTest(t)
	if _, err := c.Get(ctx, srv.URL+"/logo.png", ""); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	*now = now.Add(fresh + time.Hour)
	if img, err := c.Get(ctx, srv.URL+"/logo.png", ""); err != nil || img.Type != "image/png" {
		t.Errorf("img = %v, err = %v", img.Type, err)
	}
}

func TestPrune(t *testing.T) {
	c, srv, _, now := newTest(t)
	if _, err := c.Get(context.Background(), srv.URL+"/logo.png", ""); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(maxAge + time.Hour)
	if err := c.Prune(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(c.Dir); len(entries) != 0 {
		t.Errorf("left %d files", len(entries))
	}
}
