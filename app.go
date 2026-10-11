package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"iptv-client/internal/fetch"
	"iptv-client/internal/library"
	"iptv-client/internal/player"
	"iptv-client/internal/store"
)

// App is bound to the frontend. Every exported method is callable from
// JavaScript, so methods accept only IDs and plain values: file system
// paths are chosen through native dialogs opened from Go, never passed in
// from the UI (C3 in the design review).
type App struct {
	ctx     context.Context
	lib     *library.Library
	initErr error

	importMu     sync.Mutex
	cancelImport context.CancelFunc // non-nil while an import runs
}

// NewApp creates the application.
func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	path, err := databasePath()
	if err == nil {
		var s *store.Store
		if s, err = store.Open(ctx, path); err == nil {
			a.lib = library.New(s)
			return
		}
	}
	a.initErr = fmt.Errorf("could not open the database: %w", err)
}

func (a *App) shutdown(context.Context) {
	if a.lib != nil {
		a.lib.Store.Close()
	}
}

// ready returns the library or the startup error.
func (a *App) ready() (*library.Library, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	return a.lib, nil
}

// --- DTOs (also generate the TypeScript models) ---

// Playlist is a playlist as shown in the UI.
type Playlist struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	SourceType      string `json:"sourceType"`
	Source          string `json:"source"` // redacted for URLs
	ChannelCount    int    `json:"channelCount"`
	LastRefreshedAt int64  `json:"lastRefreshedAt"` // unix seconds, 0 = never
}

// Group is a channel group with its size.
type Group struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Channel is a channel row. It omits the stream URL and options: the UI does
// not need them and URLs usually contain account credentials.
type Channel struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Favorite bool   `json:"favorite"`
}

// ChannelQuery selects channels for the list.
type ChannelQuery struct {
	PlaylistID    int64  `json:"playlistId"`
	AllGroups     bool   `json:"allGroups"`
	Group         string `json:"group"` // used when AllGroups is false
	FavoritesOnly bool   `json:"favoritesOnly"`
	Search        string `json:"search"`
}

// ImportResult reports an add or refresh.
type ImportResult struct {
	Playlist    *Playlist `json:"playlist"` // nil when the user cancelled a dialog
	Imported    int       `json:"imported"`
	Skipped     int       `json:"skipped"`
	NotModified bool      `json:"notModified"`
	// TooSoonMinutes > 0 means the refresh was refused by the rate limit;
	// the UI may offer to force it.
	TooSoonMinutes int `json:"tooSoonMinutes"`
}

// Settings is the settings dialog state.
type Settings struct {
	VLCPath      string `json:"vlcPath"`      // resolved path, "" if not found
	VLCOverride  bool   `json:"vlcOverride"`  // true if chosen by the user
	VLCError     string `json:"vlcError"`     // why VLC was not found
	UserAgent    string `json:"userAgent"`    // "" = default
	DefaultAgent string `json:"defaultAgent"` // shown as placeholder
	DataDir      string `json:"dataDir"`
}

// ImportProgressEvent is emitted while importing with the running count.
const ImportProgressEvent = "import-progress"

// --- playlists ---

// ListPlaylists returns all playlists.
func (a *App) ListPlaylists() ([]Playlist, error) {
	l, err := a.ready()
	if err != nil {
		return nil, err
	}
	ps, err := l.Store.ListPlaylists(a.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Playlist, 0, len(ps))
	for _, p := range ps {
		out = append(out, toPlaylist(p))
	}
	return out, nil
}

// AddPlaylistURL imports a playlist from an http(s) URL.
func (a *App) AddPlaylistURL(name, url string) (ImportResult, error) {
	return a.add(name, url)
}

// AddPlaylistFile asks the user for a playlist file and imports it.
// Returns an empty result if the dialog is cancelled.
func (a *App) AddPlaylistFile(name string) (ImportResult, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose a playlist",
		Filters: []runtime.FileFilter{
			{DisplayName: "Playlists (*.m3u, *.m3u8)", Pattern: "*.m3u;*.m3u8"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return ImportResult{}, err
	}
	return a.add(name, path)
}

func (a *App) add(name, source string) (ImportResult, error) {
	l, err := a.ready()
	if err != nil {
		return ImportResult{}, err
	}
	ctx, done, err := a.beginImport()
	if err != nil {
		return ImportResult{}, err
	}
	defer done()
	p, res, err := l.AddPlaylist(ctx, name, source, a.progress)
	if err != nil {
		return ImportResult{}, friendly(err)
	}
	dto := toPlaylist(p)
	return ImportResult{Playlist: &dto, Imported: res.Imported, Skipped: res.Skipped}, nil
}

// RefreshPlaylist re-imports a playlist. Without force, URL playlists are
// rate limited and TooSoonMinutes is set instead of returning an error.
func (a *App) RefreshPlaylist(id int64, force bool) (ImportResult, error) {
	l, err := a.ready()
	if err != nil {
		return ImportResult{}, err
	}
	ctx, done, err := a.beginImport()
	if err != nil {
		return ImportResult{}, err
	}
	defer done()
	res, err := l.Refresh(ctx, id, force, a.progress)
	var tooSoon *library.TooSoonError
	if errors.As(err, &tooSoon) {
		return ImportResult{TooSoonMinutes: max(1, int(tooSoon.Remaining.Round(time.Minute).Minutes()))}, nil
	}
	if err != nil {
		return ImportResult{Skipped: res.Skipped}, friendly(err)
	}
	p, err := l.Store.GetPlaylist(a.ctx, id)
	if err != nil {
		return ImportResult{}, err
	}
	dto := toPlaylist(p)
	return ImportResult{Playlist: &dto, Imported: res.Imported, Skipped: res.Skipped, NotModified: res.NotModified}, nil
}

// CancelImport stops a running add/refresh. The previous data is kept.
func (a *App) CancelImport() {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if a.cancelImport != nil {
		a.cancelImport()
	}
}

// RenamePlaylist changes a playlist's name.
func (a *App) RenamePlaylist(id int64, name string) error {
	l, err := a.ready()
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("name cannot be empty")
	}
	return l.Store.RenamePlaylist(a.ctx, id, name)
}

// DeletePlaylist removes a playlist and its favorites.
func (a *App) DeletePlaylist(id int64) error {
	l, err := a.ready()
	if err != nil {
		return err
	}
	return l.Store.DeletePlaylist(a.ctx, id)
}

// --- channels ---

// ListGroups returns a playlist's groups in playlist order.
func (a *App) ListGroups(playlistID int64) ([]Group, error) {
	l, err := a.ready()
	if err != nil {
		return nil, err
	}
	gs, err := l.Store.ListGroups(a.ctx, playlistID)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(gs))
	for _, g := range gs {
		out = append(out, Group{Name: g.Name, Count: g.Count})
	}
	return out, nil
}

// ChannelIDs returns the IDs matching q in display order. The UI loads the
// visible rows with Channels.
func (a *App) ChannelIDs(q ChannelQuery) ([]int64, error) {
	l, err := a.ready()
	if err != nil {
		return nil, err
	}
	f := store.ChannelFilter{PlaylistID: q.PlaylistID, FavoritesOnly: q.FavoritesOnly, Query: q.Search}
	if !q.AllGroups {
		f.Group = &q.Group
	}
	return l.Store.ChannelIDs(a.ctx, f)
}

// Channels returns rows for up to 500 IDs, in the order given.
func (a *App) Channels(ids []int64) ([]Channel, error) {
	l, err := a.ready()
	if err != nil {
		return nil, err
	}
	cs, err := l.Store.ChannelsByIDs(a.ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(cs))
	for _, c := range cs {
		out = append(out, Channel{ID: c.ID, Name: c.Name, Group: c.Group, Favorite: c.Favorite})
	}
	return out, nil
}

// SetFavorite marks or unmarks a channel as favorite.
func (a *App) SetFavorite(channelID int64, favorite bool) error {
	l, err := a.ready()
	if err != nil {
		return err
	}
	return l.Store.SetFavorite(a.ctx, channelID, favorite)
}

// Play opens a channel in VLC.
func (a *App) Play(channelID int64) error {
	l, err := a.ready()
	if err != nil {
		return err
	}
	return friendly(l.Play(a.ctx, channelID))
}

// --- settings ---

// GetSettings returns the current settings.
func (a *App) GetSettings() (Settings, error) {
	l, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	override, err := l.Store.GetSetting(a.ctx, library.SettingVLCPath)
	if err != nil {
		return Settings{}, err
	}
	ua, err := l.Store.GetSetting(a.ctx, library.SettingUserAgent)
	if err != nil {
		return Settings{}, err
	}
	s := Settings{VLCOverride: override != "", UserAgent: ua, DefaultAgent: fetch.DefaultUserAgent}
	if s.VLCPath, err = l.VLCPath(a.ctx); err != nil {
		s.VLCError = err.Error()
	}
	if p, err := databasePath(); err == nil {
		s.DataDir = filepath.Dir(p)
	}
	return s, nil
}

// ChooseVLC asks the user to locate the VLC executable and saves it.
func (a *App) ChooseVLC() (Settings, error) {
	l, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Locate VLC",
		Filters: vlcFilters(),
	})
	if err != nil {
		return Settings{}, err
	}
	if path != "" {
		if err := l.SetVLCPath(a.ctx, vlcExecutable(path)); err != nil {
			return Settings{}, err
		}
	}
	return a.GetSettings()
}

// ResetVLC returns to automatic VLC detection.
func (a *App) ResetVLC() (Settings, error) {
	l, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	if err := l.SetVLCPath(a.ctx, ""); err != nil {
		return Settings{}, err
	}
	return a.GetSettings()
}

// SetUserAgent sets the User-Agent for playlist downloads ("" = default).
func (a *App) SetUserAgent(ua string) error {
	l, err := a.ready()
	if err != nil {
		return err
	}
	return l.SetUserAgent(a.ctx, ua)
}

// --- helpers ---

// DatabaseEnv overrides the database location (development and testing).
const DatabaseEnv = "IPTV_CLIENT_DB"

func databasePath() (string, error) {
	if p := os.Getenv(DatabaseEnv); p != "" {
		return filepath.Abs(p)
	}
	return store.DefaultPath()
}

// beginImport allows one import at a time and returns its cancellable context.
func (a *App) beginImport() (context.Context, func(), error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	if a.cancelImport != nil {
		return nil, nil, errors.New("another playlist is being imported; wait for it to finish")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	a.cancelImport = cancel
	return ctx, func() {
		a.importMu.Lock()
		defer a.importMu.Unlock()
		cancel()
		a.cancelImport = nil
	}, nil
}

func (a *App) progress(n int) { runtime.EventsEmit(a.ctx, ImportProgressEvent, n) }

func toPlaylist(p store.Playlist) Playlist {
	src := p.Source
	if p.SourceType == store.SourceURL {
		src = fetch.Redact(src)
	}
	var refreshed int64
	if !p.LastRefreshedAt.IsZero() {
		refreshed = p.LastRefreshedAt.Unix()
	}
	return Playlist{
		ID: p.ID, Name: p.Name, SourceType: string(p.SourceType), Source: src,
		ChannelCount: p.ChannelCount, LastRefreshedAt: refreshed,
	}
}

// friendly rewrites errors that would otherwise read poorly in the UI.
func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return errors.New("import cancelled; the previous channel list was kept")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("import timed out; the previous channel list was kept")
	case errors.Is(err, fetch.ErrTooLarge):
		return fmt.Errorf("%w (limit %d MB)", err, fetch.DefaultMaxBytes>>20)
	case errors.Is(err, player.ErrNotFound):
		return errors.New("VLC was not found. Install it from videolan.org, or choose its location in Settings")
	}
	return err
}
