package playlist

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"iptv-client/internal/channel"
	"iptv-client/internal/database"
	"iptv-client/internal/epg"
	"iptv-client/internal/fetch"
	"iptv-client/internal/m3u"
	"iptv-client/internal/player"
	"iptv-client/internal/setting"
)

// DefaultMinRefreshInterval protects provider accounts from bans caused by
// downloading the playlist too often (H6 in the review).
const DefaultMinRefreshInterval = time.Hour

// progressEvery is how many entries are imported between progress callbacks.
const progressEvery = 1000

// Service adds, refreshes, renames and deletes playlists.
type Service struct {
	db                 *database.DB
	repo               *Repository
	settings           *setting.Service
	MinRefreshInterval time.Duration
	Now                func() time.Time
}

// NewService returns a Service backed by db that downloads with the
// User-Agent from settings.
func NewService(db *database.DB, settings *setting.Service) *Service {
	return &Service{
		db:                 db,
		repo:               NewRepository(db),
		settings:           settings,
		MinRefreshInterval: DefaultMinRefreshInterval,
		Now:                time.Now,
	}
}

// RefreshResult describes the outcome of an import.
type RefreshResult struct {
	Imported    int  // channels stored
	Skipped     int  // entries dropped because their URL is not playable
	NotModified bool // server said nothing changed; channels untouched
	HLS         bool // source was a single HLS stream
}

// TooSoonError is returned when a refresh is attempted within the minimum
// interval since the last successful one.
type TooSoonError struct{ Remaining time.Duration }

func (e *TooSoonError) Error() string {
	return fmt.Sprintf("playlist was refreshed recently; try again in %d min (refreshing too often can get your provider account blocked)",
		e.Minutes())
}

// Minutes is the remaining wait rounded to whole minutes, at least 1.
func (e *TooSoonError) Minutes() int {
	return max(1, int(e.Remaining.Round(time.Minute).Minutes()))
}

// ErrNoChannels is returned when a source contains no playable channels.
// The previously stored channels are kept.
var ErrNoChannels = errors.New("the playlist contains no playable channels")

// List returns all playlists ordered by name.
func (s *Service) List(ctx context.Context) ([]Playlist, error) {
	return s.repo.List(ctx)
}

// Get returns one playlist.
func (s *Service) Get(ctx context.Context, id int64) (Playlist, error) {
	return s.repo.Get(ctx, id)
}

// Add registers a playlist source and imports it. source is an http(s) URL
// or a local file path. If the first import fails, nothing is saved.
func (s *Service) Add(ctx context.Context, name, source string, progress func(int)) (Playlist, RefreshResult, error) {
	source = strings.TrimSpace(source)
	typ, err := sourceType(source)
	if err != nil {
		return Playlist{}, RefreshResult{}, err
	}
	if typ == SourceFile {
		if source, err = filepath.Abs(source); err != nil {
			return Playlist{}, RefreshResult{}, err
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultName(typ, source)
	}
	id, err := s.repo.Create(ctx, name, typ, source, s.Now())
	if err != nil {
		return Playlist{}, RefreshResult{}, err
	}
	res, err := s.Refresh(ctx, id, true, progress)
	if err != nil {
		// Use a fresh context: ctx may be the reason we failed.
		_ = s.Delete(context.Background(), id)
		return Playlist{}, RefreshResult{}, err
	}
	// The import has committed: a cancel arriving now must not fail it.
	p, err := s.repo.Get(context.WithoutCancel(ctx), id)
	return p, res, err
}

// Refresh re-imports a playlist. Unless force is set, URL playlists cannot be
// refreshed more often than MinRefreshInterval. progress, if non-nil, is
// called with the running entry count.
func (s *Service) Refresh(ctx context.Context, id int64, force bool, progress func(int)) (RefreshResult, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return RefreshResult{}, err
	}
	if !force && p.SourceType == SourceURL && !p.LastRefreshedAt.IsZero() {
		if since := s.Now().Sub(p.LastRefreshedAt); since < s.MinRefreshInterval {
			return RefreshResult{}, &TooSoonError{Remaining: s.MinRefreshInterval - since}
		}
	}
	return s.refresh(ctx, p, progress)
}

// SetURL changes the URL of a URL playlist and imports from it. The new URL
// is saved only if the import succeeds; otherwise the old URL and channels
// are kept.
func (s *Service) SetURL(ctx context.Context, id int64, url string, progress func(int)) (RefreshResult, error) {
	url = strings.TrimSpace(url)
	if !fetch.IsHTTPURL(url) {
		return RefreshResult{}, errors.New("the playlist URL must be an http:// or https:// address")
	}
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return RefreshResult{}, err
	}
	if p.SourceType != SourceURL {
		return RefreshResult{}, errors.New("this playlist is loaded from a file, not a URL")
	}
	// The validators belong to the old URL.
	p.Source, p.ETag, p.LastModified = url, "", ""
	return s.refresh(ctx, p, progress)
}

// refresh imports p from p.Source.
func (s *Service) refresh(ctx context.Context, p Playlist, progress func(int)) (RefreshResult, error) {
	ua, err := s.settings.UserAgent(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	opts := fetch.Options{UserAgent: ua}
	var fr fetch.Result
	switch p.SourceType {
	case SourceURL:
		opts.ETag, opts.LastModified = p.ETag, p.LastModified
		fr, err = fetch.URL(ctx, p.Source, opts)
	case SourceFile:
		fr, err = fetch.File(p.Source, opts)
	}
	if err != nil {
		return RefreshResult{}, err
	}
	if fr.NotModified {
		return RefreshResult{NotModified: true}, s.repo.MarkRefreshed(ctx, p.ID, s.Now())
	}
	defer fr.Body.Close()

	var res RefreshResult
	err = s.replaceChannels(ctx, p.ID, p.Source, fr.ETag, fr.LastModified, func(insert func(channel.Input) error) (string, error) {
		pr, err := m3u.Parse(fr.Body, func(e m3u.Entry) error {
			if player.ValidateURL(e.URL) != nil {
				res.Skipped++
				return nil
			}
			res.Imported++
			if progress != nil && res.Imported%progressEvery == 0 {
				progress(res.Imported)
			}
			return insert(channel.Input{
				Name:    e.Name,
				URL:     e.URL,
				Group:   e.Group,
				TvgID:   e.TvgID,
				TvgName: e.TvgName,
				TvgLogo: e.TvgLogo,
				Options: player.SanitizeOptions(e.Options),
			})
		})
		if err != nil && !errors.Is(err, m3u.ErrEmpty) {
			return "", err
		}
		if pr.Kind == m3u.KindHLS {
			// The source itself is one stream.
			if p.SourceType != SourceURL || player.ValidateURL(p.Source) != nil {
				return "", errors.New("this file is a single HLS stream, not a channel list; add it by URL instead")
			}
			res.HLS, res.Imported = true, 1
			return "", insert(channel.Input{Name: p.Name, URL: p.Source})
		}
		if res.Imported == 0 {
			return "", ErrNoChannels
		}
		return guideURLFromHeader(pr.Header), nil
	})
	if err != nil {
		return RefreshResult{Skipped: res.Skipped}, err
	}
	if progress != nil {
		progress(res.Imported)
	}
	return res, nil
}

// replaceChannels atomically replaces all channels of a playlist. produce is
// called with an insert function and should feed it every channel, then
// return the playlist header's guide URL; if produce returns an error the
// transaction is rolled back and the previous channels remain.
// source (where the channels were read from) and etag/lastModified are
// stored with the new data.
func (s *Service) replaceChannels(ctx context.Context, playlistID int64, source, etag, lastModified string,
	produce func(insert func(channel.Input) error) (tvgURL string, err error)) error {

	return s.db.InTx(ctx, func(tx database.Querier) error {
		playlists := NewRepository(tx)
		before, err := playlists.Get(ctx, playlistID)
		if err != nil {
			return err
		}
		var tvgURL string
		n, err := channel.NewRepository(tx).ReplaceAllInPlaylist(ctx, playlistID, func(insert func(channel.Input) error) error {
			var err error
			tvgURL, err = produce(insert)
			return err
		})
		if err != nil {
			return err
		}
		if err := playlists.SaveRefresh(ctx, playlistID, n, source, etag, lastModified, tvgURL, s.Now()); err != nil {
			return err
		}
		after := before
		after.TvgURL = tvgURL
		return syncGuide(ctx, tx, before, after)
	})
}

// syncGuide brings the stored guide in line with a change of a playlist's
// guide source: a new source is due for download, and with none left the
// old guide is deleted, since it would never be refreshed or cleared.
func syncGuide(ctx context.Context, tx database.Querier, before, after Playlist) error {
	switch src := after.GuideSource(); {
	case src == "":
		return epg.NewRepository(tx).DeleteAllInPlaylist(ctx, after.ID)
	case src != before.GuideSource():
		return NewRepository(tx).MarkGuideStale(ctx, after.ID)
	}
	return nil
}

// ErrNoGuide is returned when refreshing the guide of a playlist that has
// no guide URL.
var ErrNoGuide = errors.New("this playlist has no program guide URL")

// SetGuideURL sets the program guide (XMLTV) URL of a playlist. An empty
// url means the one named in the playlist, if any; if there is none the
// stored guide is deleted. The new guide is not downloaded: call
// RefreshGuide.
func (s *Service) SetGuideURL(ctx context.Context, id int64, url string) error {
	url = strings.TrimSpace(url)
	if url != "" && !fetch.IsHTTPURL(url) {
		return errors.New("the program guide URL must be an http:// or https:// address")
	}
	return s.db.InTx(ctx, func(tx database.Querier) error {
		playlists := NewRepository(tx)
		before, err := playlists.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := playlists.SetGuideURL(ctx, id, url); err != nil {
			return err
		}
		after := before
		after.GuideURL = url
		return syncGuide(ctx, tx, before, after)
	})
}

// RefreshGuide downloads a playlist's program guide and replaces the stored
// one with the programmes of its channels. If it fails, the previous guide
// is kept. progress, if non-nil, is called with the running programme count.
func (s *Service) RefreshGuide(ctx context.Context, id int64, progress func(int)) (epg.ImportResult, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return epg.ImportResult{}, err
	}
	src := p.GuideSource()
	if src == "" {
		return epg.ImportResult{}, ErrNoGuide
	}
	ua, err := s.settings.UserAgent(ctx)
	if err != nil {
		return epg.ImportResult{}, err
	}
	// Uncompressed guides of big providers are hundreds of MB.
	fr, err := fetch.URL(ctx, src, fetch.Options{UserAgent: ua, MaxBytes: epg.MaxUncompressed})
	if err != nil {
		return epg.ImportResult{}, fmt.Errorf("program guide: %w", err)
	}
	defer fr.Body.Close()
	var res epg.ImportResult
	err = s.db.InTx(ctx, func(tx database.Querier) error {
		ids, names, err := channel.NewRepository(tx).GuideNames(ctx, id)
		if err != nil {
			return err
		}
		res, err = epg.NewRepository(tx).Import(ctx, id, fr.Body, epg.NewKeys(ids, names), s.Now(), progress)
		if err != nil {
			return err
		}
		return NewRepository(tx).MarkGuideRefreshed(ctx, id, s.Now())
	})
	if err != nil {
		return epg.ImportResult{}, err
	}
	if progress != nil {
		progress(res.Programmes)
	}
	return res, nil
}

// guideURLFromHeader returns the first http(s) guide URL named in a
// playlist's #EXTM3U attributes, "" if none. The attribute can hold a
// comma-separated list; only one guide per playlist is supported.
func guideURLFromHeader(header map[string]string) string {
	for _, key := range []string{"url-tvg", "x-tvg-url", "tvg-url"} {
		for _, u := range strings.Split(header[key], ",") {
			if u = strings.TrimSpace(u); fetch.IsHTTPURL(u) {
				return u
			}
		}
	}
	return ""
}

// Rename changes a playlist's display name.
func (s *Service) Rename(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name cannot be empty")
	}
	return s.repo.Rename(ctx, id, name)
}

// Delete removes a playlist with its channels and favorites.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.db.InTx(ctx, func(tx database.Querier) error {
		if err := channel.NewRepository(tx).DeleteAllInPlaylist(ctx, id); err != nil {
			return err
		}
		// Favorites and the guide go with the playlist row (ON DELETE CASCADE).
		return NewRepository(tx).Delete(ctx, id)
	})
}

func sourceType(source string) (SourceType, error) {
	lower := strings.ToLower(source)
	switch {
	case source == "":
		return "", errors.New("enter a playlist URL or choose a file")
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return SourceURL, nil
	case strings.Contains(lower, "://"):
		return "", errors.New("only http:// and https:// playlist URLs are supported")
	}
	fi, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("cannot open playlist file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("playlist path is not a file")
	}
	return SourceFile, nil
}

func defaultName(t SourceType, source string) string {
	if t == SourceFile {
		return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	if u, err := url.Parse(source); err == nil && u.Host != "" {
		return u.Host // never the user info, path or query (credentials)
	}
	return "Playlist"
}
