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
	p, res, err := s.Refresh(ctx, id, true, progress)
	if err != nil {
		// Use a fresh context: ctx may be the reason we failed.
		_ = s.Delete(context.Background(), id)
	}
	return p, res, err
}

// Refresh re-imports a playlist and returns it as updated. Unless force is
// set, URL playlists cannot be refreshed more often than MinRefreshInterval.
// progress, if non-nil, is called with the running entry count.
func (s *Service) Refresh(ctx context.Context, id int64, force bool, progress func(int)) (Playlist, RefreshResult, error) {
	res, err := s.refresh(ctx, id, force, progress)
	if err != nil {
		return Playlist{}, res, err
	}
	p, err := s.repo.Get(ctx, id)
	return p, res, err
}

func (s *Service) refresh(ctx context.Context, id int64, force bool, progress func(int)) (RefreshResult, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return RefreshResult{}, err
	}
	if !force && p.SourceType == SourceURL && !p.LastRefreshedAt.IsZero() {
		if since := s.Now().Sub(p.LastRefreshedAt); since < s.MinRefreshInterval {
			return RefreshResult{}, &TooSoonError{Remaining: s.MinRefreshInterval - since}
		}
	}

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
		return RefreshResult{NotModified: true}, s.repo.MarkRefreshed(ctx, id, s.Now())
	}
	defer fr.Body.Close()

	var res RefreshResult
	err = s.replaceChannels(ctx, id, fr.ETag, fr.LastModified, func(insert func(channel.Input) error) error {
		pr, err := m3u.Parse(fr.Body, func(e m3u.Entry) error {
			if player.ValidateURL(e.URL) != nil {
				res.Skipped++
				return nil
			}
			res.Imported++
			if progress != nil && res.Imported%progressEvery == 0 {
				progress(res.Imported)
			}
			c := channel.Input{
				Name:    e.Name,
				URL:     e.URL,
				Group:   e.Group,
				TvgID:   e.TvgID,
				TvgName: e.TvgName,
				TvgLogo: e.TvgLogo,
			}
			if len(e.Options) > 0 { // most entries have none
				c.Options = player.SanitizeOptions(e.Options)
			}
			return insert(c)
		})
		if err != nil && !errors.Is(err, m3u.ErrEmpty) {
			return err
		}
		if pr.Kind == m3u.KindHLS {
			// The source itself is one stream.
			if p.SourceType != SourceURL || player.ValidateURL(p.Source) != nil {
				return errors.New("this file is a single HLS stream, not a channel list; add it by URL instead")
			}
			res.HLS, res.Imported = true, 1
			return insert(channel.Input{Name: p.Name, URL: p.Source})
		}
		if res.Imported == 0 {
			return ErrNoChannels
		}
		return nil
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
// called with an insert function and should feed it every channel; if
// produce returns an error the transaction is rolled back and the previous
// channels remain. etag/lastModified are stored with the new data.
func (s *Service) replaceChannels(ctx context.Context, playlistID int64, etag, lastModified string,
	produce func(insert func(channel.Input) error) error) error {

	return s.db.InTx(ctx, func(tx database.Querier) error {
		playlists := NewRepository(tx)
		exists, err := playlists.Exists(ctx, playlistID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("playlist %d: %w", playlistID, database.ErrNotFound)
		}
		n, err := channel.NewRepository(tx).ReplaceAllInPlaylist(ctx, playlistID, produce)
		if err != nil {
			return err
		}
		return playlists.SaveRefresh(ctx, playlistID, n, etag, lastModified, s.Now())
	})
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
		// Favorites go with the playlist row (ON DELETE CASCADE).
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
