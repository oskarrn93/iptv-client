// Package library ties fetching, parsing, storage and playback together. It
// is the single API used by both the GUI and the debug CLI.
package library

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"iptv-client/internal/fetch"
	"iptv-client/internal/m3u"
	"iptv-client/internal/player"
	"iptv-client/internal/store"
)

// Setting keys.
const (
	SettingVLCPath   = "vlc_path"
	SettingUserAgent = "user_agent"
)

// DefaultMinRefreshInterval protects provider accounts from bans caused by
// downloading the playlist too often (H6 in the review).
const DefaultMinRefreshInterval = time.Hour

// progressEvery is how many entries are imported between progress callbacks.
const progressEvery = 1000

// Library is the application service.
type Library struct {
	Store              *store.Store
	MinRefreshInterval time.Duration
	MaxBytes           int64
	HTTPClient         *http.Client // nil = fetch package default
	Now                func() time.Time
}

// New returns a Library with default settings.
func New(s *store.Store) *Library {
	return &Library{Store: s, MinRefreshInterval: DefaultMinRefreshInterval, Now: time.Now}
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
	return fmt.Sprintf("playlist was refreshed recently; try again in %s (refreshing too often can get your provider account blocked)",
		e.Remaining.Round(time.Minute))
}

// ErrNoChannels is returned when a source contains no playable channels.
// The previously stored channels are kept.
var ErrNoChannels = errors.New("the playlist contains no playable channels")

// AddPlaylist registers a playlist source and imports it. source is an
// http(s) URL or a local file path. If the first import fails, nothing is
// saved.
func (l *Library) AddPlaylist(ctx context.Context, name, source string, progress func(int)) (store.Playlist, RefreshResult, error) {
	source = strings.TrimSpace(source)
	typ, err := sourceType(source)
	if err != nil {
		return store.Playlist{}, RefreshResult{}, err
	}
	if typ == store.SourceFile {
		if source, err = filepath.Abs(source); err != nil {
			return store.Playlist{}, RefreshResult{}, err
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultName(typ, source)
	}
	id, err := l.Store.AddPlaylist(ctx, name, typ, source)
	if err != nil {
		return store.Playlist{}, RefreshResult{}, err
	}
	res, err := l.Refresh(ctx, id, true, progress)
	if err != nil {
		// Use a fresh context: ctx may be the reason we failed.
		_ = l.Store.DeletePlaylist(context.Background(), id)
		return store.Playlist{}, res, err
	}
	p, err := l.Store.GetPlaylist(ctx, id)
	return p, res, err
}

// Refresh re-imports a playlist. Unless force is set, URL playlists cannot be
// refreshed more often than MinRefreshInterval. progress, if non-nil, is
// called with the running entry count.
func (l *Library) Refresh(ctx context.Context, id int64, force bool, progress func(int)) (RefreshResult, error) {
	p, err := l.Store.GetPlaylist(ctx, id)
	if err != nil {
		return RefreshResult{}, err
	}
	if !force && p.SourceType == store.SourceURL && !p.LastRefreshedAt.IsZero() {
		if since := l.now().Sub(p.LastRefreshedAt); since < l.MinRefreshInterval {
			return RefreshResult{}, &TooSoonError{Remaining: l.MinRefreshInterval - since}
		}
	}

	ua, err := l.Store.GetSetting(ctx, SettingUserAgent)
	if err != nil {
		return RefreshResult{}, err
	}
	opts := fetch.Options{UserAgent: ua, MaxBytes: l.MaxBytes, Client: l.HTTPClient}
	var fr fetch.Result
	switch p.SourceType {
	case store.SourceURL:
		opts.ETag, opts.LastModified = p.ETag, p.LastModified
		fr, err = fetch.URL(ctx, p.Source, opts)
	case store.SourceFile:
		fr, err = fetch.File(p.Source, opts)
	}
	if err != nil {
		return RefreshResult{}, err
	}
	if fr.NotModified {
		return RefreshResult{NotModified: true}, l.Store.MarkNotModified(ctx, id)
	}
	defer fr.Body.Close()

	var res RefreshResult
	_, err = l.Store.ReplaceChannels(ctx, id, fr.ETag, fr.LastModified, func(insert func(store.ChannelInput) error) error {
		pr, err := m3u.Parse(fr.Body, func(e m3u.Entry) error {
			if player.ValidateURL(e.URL) != nil {
				res.Skipped++
				return nil
			}
			res.Imported++
			if progress != nil && res.Imported%progressEvery == 0 {
				progress(res.Imported)
			}
			return insert(store.ChannelInput{
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
			if p.SourceType != store.SourceURL || player.ValidateURL(p.Source) != nil {
				return errors.New("this file is a single HLS stream, not a channel list; add it by URL instead")
			}
			res.HLS, res.Imported = true, 1
			return insert(store.ChannelInput{Name: p.Name, URL: p.Source})
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

// Play launches VLC for a channel.
func (l *Library) Play(ctx context.Context, channelID int64) error {
	ch, err := l.Store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	vlc, err := l.VLCPath(ctx)
	if err != nil {
		return err
	}
	// Launch re-validates the URL and options; stored data is not trusted.
	return player.Launch(vlc, ch.Name, ch.URL, ch.Options)
}

// VLCPath returns the configured VLC path, or the auto-detected one.
func (l *Library) VLCPath(ctx context.Context) (string, error) {
	override, err := l.Store.GetSetting(ctx, SettingVLCPath)
	if err != nil {
		return "", err
	}
	return player.Find(override)
}

// SetVLCPath stores a VLC location chosen by the user ("" = auto-detect).
// Only call this with a path the user picked in a native file dialog.
func (l *Library) SetVLCPath(ctx context.Context, path string) error {
	if path != "" {
		if err := player.ValidateExecutable(path); err != nil {
			return err
		}
	}
	return l.Store.SetSetting(ctx, SettingVLCPath, path)
}

// SetUserAgent stores the User-Agent used to download playlists.
func (l *Library) SetUserAgent(ctx context.Context, ua string) error {
	ua = strings.TrimSpace(ua)
	if strings.ContainsAny(ua, "\r\n") || len(ua) > 512 {
		return errors.New("invalid User-Agent")
	}
	return l.Store.SetSetting(ctx, SettingUserAgent, ua)
}

func (l *Library) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func sourceType(source string) (store.SourceType, error) {
	lower := strings.ToLower(source)
	switch {
	case source == "":
		return "", errors.New("enter a playlist URL or choose a file")
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return store.SourceURL, nil
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
	return store.SourceFile, nil
}

func defaultName(t store.SourceType, source string) string {
	if t == store.SourceFile {
		return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	host := strings.TrimSuffix(fetch.Redact(source), "/…")
	return strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
}
