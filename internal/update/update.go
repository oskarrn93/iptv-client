// Package update checks GitHub Releases for a newer version of the app and
// downloads and installs it.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"iptv-client/internal/fetch"
)

// Repo is the GitHub repository whose releases are installed.
const Repo = "oskarrn93/iptv-client"

// maxAssetBytes caps the download size; the installers are a few tens of MB.
const maxAssetBytes = 500 << 20

// ErrDevBuild is returned when the running version is not a release, so it
// cannot be compared with one.
var ErrDevBuild = errors.New("this is a development build; updates are only available in released versions")

// Release is the newest published release and this platform's asset in it.
type Release struct {
	Version  string // e.g. "v1.2.3"
	PageURL  string
	AssetURL string
	Size     int64
	SHA256   string // hex
}

// Checker finds, downloads and installs releases. The zero value uses the
// public GitHub API and the running OS's Platform.
type Checker struct {
	APIBase  string // default "https://api.github.com"
	Client   *http.Client
	Platform Platform // default Current()
}

var defaultClient = fetch.NewClient("https")

func (c *Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return defaultClient
}

func (c *Checker) platform() Platform {
	if c.Platform != nil {
		return c.Platform
	}
	return Current()
}

// Latest returns the newest non-prerelease release.
func (c *Checker) Latest(ctx context.Context, userAgent string) (Release, error) {
	name := c.platform().AssetName()
	if name == "" {
		return Release{}, errors.New("updates are not supported on this platform")
	}
	base := c.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.client().Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("could not reach GitHub: %s", fetch.Describe(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Release{}, errors.New("no published release was found")
	case resp.StatusCode != http.StatusOK:
		return Release{}, fmt.Errorf("checking for updates failed: GitHub answered %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"` // "sha256:<hex>"
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("reading the release information: %w", err)
	}
	if !semver.IsValid(body.TagName) {
		return Release{}, fmt.Errorf("the latest release has an unexpected version %q", body.TagName)
	}
	for _, a := range body.Assets {
		if a.Name != name {
			continue
		}
		sum, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || len(sum) != sha256.Size*2 {
			return Release{}, fmt.Errorf("release %s has no checksum for %s", body.TagName, name)
		}
		if !strings.HasPrefix(a.URL, "https://") {
			return Release{}, fmt.Errorf("release %s has an invalid download URL", body.TagName)
		}
		return Release{
			Version: body.TagName, PageURL: body.HTMLURL,
			AssetURL: a.URL, Size: a.Size, SHA256: strings.ToLower(sum),
		}, nil
	}
	return Release{}, fmt.Errorf("release %s has no download for this platform", body.TagName)
}

// Newer reports whether latest is a newer semantic version than current. It
// returns ErrDevBuild when current is not a version (e.g. "dev").
func Newer(current, latest string) (bool, error) {
	if !semver.IsValid(current) {
		return false, ErrDevBuild
	}
	if !semver.IsValid(latest) {
		return false, fmt.Errorf("invalid version %q", latest)
	}
	return semver.Compare(latest, current) > 0, nil
}

// Download saves r's asset into a new temporary directory, verifies its
// checksum and returns its path. progress receives the percentage done.
func (c *Checker) Download(ctx context.Context, r Release, userAgent string, progress func(int)) (string, error) {
	if r.Size <= 0 || r.Size > maxAssetBytes {
		return "", fmt.Errorf("the update has an unexpected size (%d bytes)", r.Size)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading the update: %s", fetch.Describe(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading the update failed: %s", resp.Status)
	}

	dir, err := os.MkdirTemp("", "iptv-client-update-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, c.platform().AssetName())
	if err := save(path, resp.Body, r, progress); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

func save(path string, body io.Reader, r Release, progress func(int)) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	pr := &progressReader{r: io.LimitReader(body, r.Size+1), total: r.Size, report: progress}
	n, err := io.Copy(io.MultiWriter(f, h), pr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("downloading the update: %w", err)
	case n != r.Size:
		return fmt.Errorf("the download is incomplete (%d of %d bytes)", n, r.Size)
	case hex.EncodeToString(h.Sum(nil)) != r.SHA256:
		return errors.New("the download is corrupt: its checksum does not match the release")
	}
	return nil
}

type progressReader struct {
	r      io.Reader
	n      int64
	total  int64
	last   int
	report func(int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if pct := int(p.n * 100 / p.total); p.report != nil && pct != p.last && pct <= 100 {
		p.last = pct
		p.report(pct)
	}
	return n, err
}
