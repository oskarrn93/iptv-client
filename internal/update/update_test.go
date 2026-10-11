package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
		err             error
	}{
		{"v1.2.3", "v1.2.4", true, nil},
		{"v1.2.3", "v1.10.0", true, nil},
		{"v1.2.3", "v2.0.0", true, nil},
		{"v1.2.3", "v1.2.3", false, nil},
		{"v1.10.0", "v1.9.9", false, nil},
		{"v1.3.0-rc1", "v1.3.0", true, nil},
		{"v1.3.0", "v1.3.0-rc1", false, nil},
		{"dev", "v1.0.0", false, ErrDevBuild},
		{"dev-abc1234", "v1.0.0", false, ErrDevBuild},
		{"1.2.3", "v1.2.4", false, ErrDevBuild},
	}
	for _, tt := range tests {
		got, err := Newer(tt.current, tt.latest)
		if got != tt.want || !errors.Is(err, tt.err) {
			t.Errorf("Newer(%q, %q) = %v, %v; want %v, %v", tt.current, tt.latest, got, err, tt.want, tt.err)
		}
	}
}

func TestNewerRejectsInvalidLatest(t *testing.T) {
	for _, s := range []string{"", "v", "1.2.3", "v1.2.3.4", "v1.2.x", "v01.2.3", "latest"} {
		if _, err := Newer("v1.0.0", s); err == nil {
			t.Errorf("Newer(v1.0.0, %q) accepted", s)
		}
	}
}

// server serves a latest release whose Windows asset is payload with the
// given digest.
func server(t *testing.T, payload []byte, digest string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + Repo + "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.4.0","html_url":"https://example.invalid/r","assets":[
				{"name":"iptv-client-macos-universal.dmg","browser_download_url":"%[1]s/mac","size":1,"digest":"sha256:%[2]s"},
				{"name":"iptv-client-windows-amd64-setup.exe","browser_download_url":"%[1]s/win","size":%[3]d,"digest":"%[4]s"}]}`,
				srv.URL, strings.Repeat("0", 64), len(payload), digest)
		case "/win":
			w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestLatestAndDownload(t *testing.T) {
	payload := []byte("installer bytes")
	srv := server(t, payload, "sha256:"+sum(payload))
	c := &Checker{APIBase: srv.URL, Client: srv.Client(), Platform: ForOS("windows")}

	r, err := c.Latest(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "v1.4.0" || r.AssetURL != srv.URL+"/win" || r.Size != int64(len(payload)) {
		t.Fatalf("unexpected release %+v", r)
	}

	var last int
	path, err := c.Download(context.Background(), r, "test", func(p int) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(strings.TrimSuffix(path, ForOS("windows").AssetName())) })
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("downloaded %q, %v", got, err)
	}
	if last != 100 {
		t.Errorf("last progress = %d, want 100", last)
	}
}

func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	payload := []byte("tampered bytes")
	srv := server(t, payload, "sha256:"+sum([]byte("original")))
	c := &Checker{APIBase: srv.URL, Client: srv.Client(), Platform: ForOS("windows")}
	r, err := c.Latest(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(context.Background(), r, "test", nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want checksum error", err)
	}
}

func TestLatestRequiresDigest(t *testing.T) {
	srv := server(t, []byte("x"), "")
	c := &Checker{APIBase: srv.URL, Client: srv.Client(), Platform: ForOS("windows")}
	if _, err := c.Latest(context.Background(), "test"); err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Fatalf("err = %v, want missing checksum error", err)
	}
}

func TestLatestUnsupportedPlatform(t *testing.T) {
	c := &Checker{Platform: ForOS("linux")}
	if _, err := c.Latest(context.Background(), "test"); err == nil {
		t.Fatal("expected an error on linux")
	}
}

func TestForOS(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "iptv-client-windows-amd64-setup.exe",
		"darwin":  "iptv-client-macos-universal.dmg",
		"linux":   "",
	} {
		if got := ForOS(goos).AssetName(); got != want {
			t.Errorf("ForOS(%q).AssetName() = %q, want %q", goos, got, want)
		}
	}
	if err := ForOS("linux").Install("x"); err == nil {
		t.Error("unsupported platform installed")
	}
}
