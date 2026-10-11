// Package fetch loads playlist data from an HTTP(S) URL or a local file.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultMaxBytes caps how much playlist data is read (L4 in the review).
const DefaultMaxBytes = 200 << 20

// DefaultUserAgent is sent when no other User-Agent is configured. Some
// providers reject Go's default "Go-http-client".
const DefaultUserAgent = "VLC/3.0.21 LibVLC/3.0.21"

// Options configures a fetch.
type Options struct {
	UserAgent    string
	MaxBytes     int64
	ETag         string // sent as If-None-Match
	LastModified string // sent as If-Modified-Since
	Client       *http.Client
}

// Result is an opened playlist. Body is nil when NotModified is true.
type Result struct {
	Body         io.ReadCloser
	ETag         string
	LastModified string
	NotModified  bool
}

// ErrTooLarge is returned by Body.Read once MaxBytes is exceeded.
var ErrTooLarge = errors.New("playlist is larger than the allowed maximum")

var defaultClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		// Go transparently requests and decodes gzip when this is false.
		DisableCompression: false,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
		}
		return nil
	},
}

// URL opens a playlist over HTTP(S). The caller must close Result.Body.
// Errors never contain the URL's path, query or user info, which commonly
// hold account credentials.
func URL(ctx context.Context, rawURL string, o Options) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Result{}, errors.New("playlist URL must be an http:// or https:// address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("fetching %s: invalid request", Redact(rawURL))
	}
	ua := o.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if o.ETag != "" {
		req.Header.Set("If-None-Match", o.ETag)
	}
	if o.LastModified != "" {
		req.Header.Set("If-Modified-Since", o.LastModified)
	}
	client := o.Client
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("fetching %s: %s", Redact(rawURL), describe(err))
	}
	switch {
	case resp.StatusCode == http.StatusNotModified:
		resp.Body.Close()
		return Result{NotModified: true, ETag: o.ETag, LastModified: o.LastModified}, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		resp.Body.Close()
		return Result{}, fmt.Errorf("fetching %s: server responded %s", Redact(rawURL), resp.Status)
	}
	if resp.ContentLength > maxBytes(o) {
		resp.Body.Close()
		return Result{}, ErrTooLarge
	}
	return Result{
		Body:         limit(resp.Body, maxBytes(o)),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// File opens a local playlist file. The caller must close Result.Body.
func File(path string, o Options) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > maxBytes(o) {
		f.Close()
		return Result{}, ErrTooLarge
	}
	return Result{Body: limit(f, maxBytes(o))}, nil
}

// Redact returns scheme://host/… for display, hiding anything that may be a
// credential (user info, path, query).
func Redact(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "<playlist URL>"
	}
	s := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		s += "/…"
	}
	return s
}

// describe turns a transport error into a short message without the URL.
func describe(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	msg := err.Error()
	// Defensive: never echo anything URL-like back.
	if strings.Contains(msg, "://") {
		return "network error"
	}
	return msg
}

func maxBytes(o Options) int64 {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return DefaultMaxBytes
}

type limitedBody struct {
	r      io.Reader
	c      io.Closer
	remain int64
}

func limit(rc io.ReadCloser, n int64) io.ReadCloser {
	// Read one extra byte so "exactly n" and "more than n" can be told apart.
	return &limitedBody{r: io.LimitReader(rc, n+1), c: rc, remain: n}
}

func (l *limitedBody) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.remain -= int64(n)
	if l.remain < 0 {
		return 0, ErrTooLarge
	}
	return n, err
}

func (l *limitedBody) Close() error { return l.c.Close() }
