package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"iptv-client/internal/fetch"
	"iptv-client/internal/m3u"
	"iptv-client/internal/model"
	"iptv-client/internal/player"
	"iptv-client/internal/repository"
)

// DefaultMinRefreshInterval protects provider accounts from bans caused by
// downloading the playlist too often (H6 in the review).
const DefaultMinRefreshInterval = time.Hour

// progressEvery is how many entries are imported between progress callbacks.
const progressEvery = 1000

// PlaylistService adds, refreshes, renames and deletes playlists.
type PlaylistService struct {
	db                 *repository.Database
	MinRefreshInterval time.Duration
	Now                func() time.Time
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
func (s *PlaylistService) List(ctx context.Context) ([]model.Playlist, error) {
	return s.db.Playlists.List(ctx)
}

// Get returns one playlist.
func (s *PlaylistService) Get(ctx context.Context, id int64) (model.Playlist, error) {
	return s.db.Playlists.Get(ctx, id)
}

// Add registers a playlist source and imports it. source is an http(s) URL
// or a local file path. If the first import fails, nothing is saved.
func (s *PlaylistService) Add(ctx context.Context, name, source string, progress func(int)) (model.Playlist, RefreshResult, error) {
	source = strings.TrimSpace(source)
	typ, err := sourceType(source)
	if err != nil {
		return model.Playlist{}, RefreshResult{}, err
	}
	if typ == model.SourceFile {
		if source, err = filepath.Abs(source); err != nil {
			return model.Playlist{}, RefreshResult{}, err
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultName(typ, source)
	}
	id, err := s.db.Playlists.Create(ctx, name, typ, source, s.Now())
	if err != nil {
		return model.Playlist{}, RefreshResult{}, err
	}
	res, err := s.Refresh(ctx, id, true, progress)
	if err != nil {
		// Use a fresh context: ctx may be the reason we failed.
		_ = s.Delete(context.Background(), id)
		return model.Playlist{}, res, err
	}
	p, err := s.db.Playlists.Get(ctx, id)
	return p, res, err
}

// Refresh re-imports a playlist. Unless force is set, URL playlists cannot be
// refreshed more often than MinRefreshInterval. progress, if non-nil, is
// called with the running entry count.
func (s *PlaylistService) Refresh(ctx context.Context, id int64, force bool, progress func(int)) (RefreshResult, error) {
	p, err := s.db.Playlists.Get(ctx, id)
	if err != nil {
		return RefreshResult{}, err
	}
	if !force && p.SourceType == model.SourceURL && !p.LastRefreshedAt.IsZero() {
		if since := s.Now().Sub(p.LastRefreshedAt); since < s.MinRefreshInterval {
			return RefreshResult{}, &TooSoonError{Remaining: s.MinRefreshInterval - since}
		}
	}

	ua, err := s.db.Settings.Get(ctx, SettingUserAgent)
	if err != nil {
		return RefreshResult{}, err
	}
	opts := fetch.Options{UserAgent: ua}
	var fr fetch.Result
	switch p.SourceType {
	case model.SourceURL:
		opts.ETag, opts.LastModified = p.ETag, p.LastModified
		fr, err = fetch.URL(ctx, p.Source, opts)
	case model.SourceFile:
		fr, err = fetch.File(p.Source, opts)
	}
	if err != nil {
		return RefreshResult{}, err
	}
	if fr.NotModified {
		return RefreshResult{NotModified: true}, s.db.Playlists.MarkRefreshed(ctx, id, s.Now())
	}
	defer fr.Body.Close()

	var res RefreshResult
	err = s.replaceChannels(ctx, id, fr.ETag, fr.LastModified, func(insert func(model.ChannelInput) error) error {
		pr, err := m3u.Parse(fr.Body, func(e m3u.Entry) error {
			if player.ValidateURL(e.URL) != nil {
				res.Skipped++
				return nil
			}
			res.Imported++
			if progress != nil && res.Imported%progressEvery == 0 {
				progress(res.Imported)
			}
			return insert(model.ChannelInput{
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
			return err
		}
		if pr.Kind == m3u.KindHLS {
			// The source itself is one stream.
			if p.SourceType != model.SourceURL || player.ValidateURL(p.Source) != nil {
				return errors.New("this file is a single HLS stream, not a channel list; add it by URL instead")
			}
			res.HLS, res.Imported = true, 1
			return insert(model.ChannelInput{Name: p.Name, URL: p.Source})
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
func (s *PlaylistService) replaceChannels(ctx context.Context, playlistID int64, etag, lastModified string,
	produce func(insert func(model.ChannelInput) error) error) error {

	return s.db.InTx(ctx, func(tx repository.Repositories) error {
		exists, err := tx.Playlists.Exists(ctx, playlistID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("playlist %d: %w", playlistID, repository.ErrNotFound)
		}
		if err := tx.Channels.DeleteAllInPlaylist(ctx, playlistID); err != nil {
			return err
		}
		inserter, err := tx.Channels.NewInserter(ctx, playlistID)
		if err != nil {
			return err
		}
		defer inserter.Close()
		insert := func(c model.ChannelInput) error { return inserter.Insert(ctx, c) }
		if err := produce(insert); err != nil {
			return err
		}
		if err := inserter.Flush(ctx); err != nil {
			return err
		}
		if err := tx.Channels.IndexAllInPlaylist(ctx, playlistID); err != nil {
			return err
		}
		return tx.Playlists.SaveRefresh(ctx, playlistID, inserter.Count(), etag, lastModified, s.Now())
	})
}

// Rename changes a playlist's display name.
func (s *PlaylistService) Rename(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name cannot be empty")
	}
	return s.db.Playlists.Rename(ctx, id, name)
}

// Delete removes a playlist with its channels and favorites.
func (s *PlaylistService) Delete(ctx context.Context, id int64) error {
	return s.db.InTx(ctx, func(tx repository.Repositories) error {
		if err := tx.Channels.DeleteAllInPlaylist(ctx, id); err != nil {
			return err
		}
		// Favorites go with the playlist row (ON DELETE CASCADE).
		return tx.Playlists.Delete(ctx, id)
	})
}

func sourceType(source string) (model.SourceType, error) {
	lower := strings.ToLower(source)
	switch {
	case source == "":
		return "", errors.New("enter a playlist URL or choose a file")
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return model.SourceURL, nil
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
	return model.SourceFile, nil
}

func defaultName(t model.SourceType, source string) string {
	if t == model.SourceFile {
		return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	if u, err := url.Parse(source); err == nil && u.Host != "" {
		return u.Host // never the user info, path or query (credentials)
	}
	return "Playlist"
}
