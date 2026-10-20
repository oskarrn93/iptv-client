// Package logo downloads and caches channel logos. The UI never loads a logo
// URL itself: the CSP forbids remote images, and the URLs come from
// untrusted playlists and may carry credentials. Instead the app serves
// logos from this cache under its own origin.
package logo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"iptv-client/internal/fetch"
)

const (
	// MaxBytes is the largest logo accepted.
	MaxBytes = 2 << 20
	// fresh is how long a cached logo is used before it is downloaded again;
	// failures (including being offline) are retried after failedFor.
	fresh     = 7 * 24 * time.Hour
	failedFor = time.Hour
	// maxAge is when Prune deletes an unused cache file.
	maxAge = 30 * 24 * time.Hour
	// Logos load as rows scroll into view; don't flood the logo hosts.
	maxDownloads = 6
	timeout      = 20 * time.Second
)

// ErrNoLogo means there is no usable logo: the download failed recently or
// was not an image.
var ErrNoLogo = errors.New("no logo")

// Image is a logo and its media type.
type Image struct {
	Data []byte
	Type string
}

// Cache keeps logos in files under Dir, named by a hash of the URL. An
// empty file records a failed download.
type Cache struct {
	Dir    string
	Client *http.Client
	Now    func() time.Time

	slots    chan struct{}
	mu       sync.Mutex
	inflight map[string]*download
}

type download struct {
	done chan struct{}
	img  Image
	err  error
}

// New returns a cache in dir.
func New(dir string) *Cache {
	return &Cache{
		Dir:      dir,
		Client:   fetch.NewClient("http", "https"),
		Now:      time.Now,
		slots:    make(chan struct{}, maxDownloads),
		inflight: map[string]*download{},
	}
}

// Get returns the logo at rawURL, downloading it with userAgent if it is not
// cached or is stale. Concurrent requests for one URL share a download.
// Errors never contain the URL.
func (c *Cache) Get(ctx context.Context, rawURL, userAgent string) (Image, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Image{}, ErrNoLogo
	}
	sum := sha256.Sum256([]byte(rawURL))
	path := filepath.Join(c.Dir, hex.EncodeToString(sum[:]))

	cached, cachedErr := c.read(path)
	if cachedErr == nil || errors.Is(cachedErr, ErrNoLogo) {
		return cached, cachedErr
	}

	c.mu.Lock()
	d, ok := c.inflight[path]
	if !ok {
		d = &download{done: make(chan struct{})}
		c.inflight[path] = d
		// Not tied to ctx: an image request is dropped when its row scrolls
		// away, but the logo is worth caching anyway.
		go c.download(context.WithoutCancel(ctx), d, path, rawURL, userAgent)
	}
	c.mu.Unlock()

	select {
	case <-d.done:
	case <-ctx.Done():
		return Image{}, ctx.Err()
	}
	if d.err != nil && cached.Data != nil {
		return cached, nil // stale beats nothing
	}
	return d.img, d.err
}

// read returns a cached logo. A stale one is returned with errStale; a
// recent failure with ErrNoLogo.
func (c *Cache) read(path string) (Image, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Image{}, err
	}
	age := c.Now().Sub(fi.ModTime())
	if fi.Size() == 0 {
		if age < failedFor {
			return Image{}, ErrNoLogo
		}
		return Image{}, errStale
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Image{}, err
	}
	img, err := image(data)
	if err == nil && age >= fresh {
		err = errStale
	}
	return img, err
}

var errStale = errors.New("stale")

func (c *Cache) download(ctx context.Context, d *download, path, rawURL, userAgent string) {
	defer func() {
		c.mu.Lock()
		delete(c.inflight, path)
		c.mu.Unlock()
		close(d.done)
	}()
	c.slots <- struct{}{}
	defer func() { <-c.slots }()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d.img, d.err = c.fetch(ctx, rawURL, userAgent)
	switch {
	case d.err == nil:
		c.write(path, d.img.Data)
	case c.hasData(path):
		// Keep the stale logo, and try again after failedFor.
		at := c.Now().Add(failedFor - fresh)
		os.Chtimes(path, at, at)
	default:
		c.write(path, nil)
	}
}

func (c *Cache) hasData(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

func (c *Cache) fetch(ctx context.Context, rawURL, userAgent string) (Image, error) {
	fr, err := fetch.URL(ctx, rawURL, fetch.Options{UserAgent: userAgent, MaxBytes: MaxBytes, Client: c.Client})
	if err != nil {
		return Image{}, ErrNoLogo
	}
	defer fr.Body.Close()
	data, err := io.ReadAll(fr.Body)
	if err != nil {
		return Image{}, ErrNoLogo
	}
	return image(data)
}

// write stores data atomically. Errors are ignored: the cache is an
// optimization.
func (c *Cache) write(path string, data []byte) {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(c.Dir, "tmp-*")
	if err != nil {
		return
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
}

// image checks that data is a raster image format the webview shows, by its
// content rather than the server's Content-Type. SVG is refused: it is a
// document that can reference other resources.
func image(data []byte) (Image, error) {
	switch t := http.DetectContentType(data); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/x-icon", "image/avif":
		return Image{Data: data, Type: t}, nil
	}
	return Image{}, ErrNoLogo
}

// Prune deletes cache files that have not been refreshed for a month, and
// temporary files left by an interrupted write.
func (c *Cache) Prune() error {
	entries, err := os.ReadDir(c.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		age := c.Now().Sub(fi.ModTime())
		if age > maxAge || (strings.HasPrefix(e.Name(), "tmp-") && age > time.Hour) {
			os.Remove(filepath.Join(c.Dir, e.Name()))
		}
	}
	return nil
}
