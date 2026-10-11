package fetch

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readAll(t *testing.T, r Result) string {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestURLBasicsAndConditional(t *testing.T) {
	var gotUA, gotINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotINM = r.Header.Get("User-Agent"), r.Header.Get("If-None-Match")
		if gotINM == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 01 Jan 2026 00:00:00 GMT")
		io.WriteString(w, "#EXTM3U\n")
	}))
	defer srv.Close()
	ctx := context.Background()

	r, err := URL(ctx, srv.URL+"/get.php?username=u&password=p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if body := readAll(t, r); body != "#EXTM3U\n" || r.ETag != `"v1"` || r.LastModified == "" {
		t.Errorf("got body=%q etag=%q lm=%q", body, r.ETag, r.LastModified)
	}
	if gotUA != DefaultUserAgent {
		t.Errorf("UA = %q", gotUA)
	}

	r, err = URL(ctx, srv.URL, Options{ETag: `"v1"`, UserAgent: "Custom/1"})
	if err != nil || !r.NotModified || r.Body != nil || r.ETag != `"v1"` {
		t.Fatalf("conditional = %+v, %v", r, err)
	}
	if gotUA != "Custom/1" || gotINM != `"v1"` {
		t.Errorf("UA=%q INM=%q", gotUA, gotINM)
	}
}

func TestURLGzip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			t.Error("client did not request gzip")
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		io.WriteString(gz, "compressed")
		gz.Close()
	}))
	defer srv.Close()
	r, err := URL(context.Background(), srv.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != "compressed" {
		t.Errorf("got %q", got)
	}
}

func TestURLErrorsDoNotLeakCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	secret := "/live/myuser/mypass/get.php?username=myuser&password=mypass"

	_, err := URL(context.Background(), srv.URL+secret, Options{})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want 403", err)
	}
	if strings.Contains(err.Error(), "myuser") || strings.Contains(err.Error(), "mypass") {
		t.Errorf("error leaks credentials: %v", err)
	}

	// Connection failure (closed port).
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	_, err = URL(context.Background(), "http://user:pw@"+strings.TrimPrefix(deadURL, "http://")+secret, Options{})
	if err == nil || strings.Contains(err.Error(), "myuser") || strings.Contains(err.Error(), "pw@") {
		t.Errorf("connection error = %v", err)
	}
}

func TestURLRejectsBadSchemes(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://x/y", "notaurl", "http://"} {
		if _, err := URL(context.Background(), u, Options{}); err == nil {
			t.Errorf("URL(%q) succeeded", u)
		}
	}
}

func TestURLCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := URL(ctx, srv.URL, Options{})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want timed out", err)
	}
}

func TestSizeLimit(t *testing.T) {
	body := strings.Repeat("x", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("chunked") == "" {
			w.Header().Set("Content-Length", "100")
		} else {
			w.(http.Flusher).Flush() // forces chunked encoding: no Content-Length
		}
		io.WriteString(w, body)
	}))
	defer srv.Close()
	ctx := context.Background()

	if _, err := URL(ctx, srv.URL, Options{MaxBytes: 50}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Content-Length check: err = %v", err)
	}
	r, err := URL(ctx, srv.URL+"?chunked=1", Options{MaxBytes: 50})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(r.Body)
	r.Body.Close()
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("streaming check: err = %v", err)
	}
	r, err = URL(ctx, srv.URL+"?chunked=1", Options{MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != body {
		t.Errorf("exactly-at-limit read %d bytes", len(got))
	}
}

func TestFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.m3u")
	os.WriteFile(p, []byte("#EXTM3U\n"), 0o600)
	r, err := File(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != "#EXTM3U\n" {
		t.Errorf("got %q", got)
	}
	if _, err := File(p, Options{MaxBytes: 3}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	if _, err := File(filepath.Join(t.TempDir(), "missing"), Options{}); err == nil {
		t.Error("missing file opened")
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"http://user:pw@host.com:8080/get.php?username=u&password=p": "http://host.com:8080/…",
		"https://host.com/":    "https://host.com",
		"https://host.com":     "https://host.com",
		"http://host/live/u/p": "http://host/…",
		"garbage":              "<playlist URL>",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
