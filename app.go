package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"iptv-client/internal/channel"
	"iptv-client/internal/database"
	"iptv-client/internal/fetch"
	"iptv-client/internal/player"
	"iptv-client/internal/playlist"
	"iptv-client/internal/setting"
)

// App is bound to the frontend. Every exported method is callable from
// JavaScript, so methods accept only IDs and plain values: file system
// paths are chosen through native dialogs opened from Go, never passed in
// from the UI (C3 in the design review).
type App struct {
	ctx     context.Context
	db      *database.DB
	svc     *services
	initErr error

	importMu     sync.Mutex
	cancelImport context.CancelFunc // non-nil while an import runs
}

// services are the domain services the bound methods call.
type services struct {
	playlists *playlist.Service
	channels  *channel.Service
	settings  *setting.Service
}

func newServices(db *database.DB) *services {
	settings := setting.NewService(db)
	return &services{
		playlists: playlist.NewService(db, settings),
		channels:  channel.NewService(db, settings),
		settings:  settings,
	}
}

// NewApp creates the application.
func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	path, err := database.ResolvePath()
	if err == nil {
		var db *database.DB
		if db, err = database.Open(ctx, path); err == nil {
			a.db = db
			a.svc = newServices(db)
			return
		}
	}
	a.initErr = fmt.Errorf("could not open the database: %w", err)
}

func (a *App) shutdown(context.Context) {
	if a.db != nil {
		a.db.Close()
	}
}

// ready returns the services or the startup error.
func (a *App) ready() (*services, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	return a.svc, nil
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
	PlayerPath     string `json:"playerPath"`     // resolved path, "" if not found
	PlayerName     string `json:"playerName"`     // e.g. "VLC"
	PlayerOverride bool   `json:"playerOverride"` // true if chosen by the user
	PlayerError    string `json:"playerError"`    // why no player was found
	UserAgent      string `json:"userAgent"`      // "" = default
	DefaultAgent   string `json:"defaultAgent"`   // shown as placeholder
	DataDir        string `json:"dataDir"`
	Version        string `json:"version"`
}

// ImportProgressEvent is emitted while importing with the running count.
const ImportProgressEvent = "import-progress"

// --- playlists ---

// ListPlaylists returns all playlists.
func (a *App) ListPlaylists() ([]Playlist, error) {
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	ps, err := svc.playlists.List(a.ctx)
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
	svc, err := a.ready()
	if err != nil {
		return ImportResult{}, err
	}
	ctx, done, err := a.beginImport()
	if err != nil {
		return ImportResult{}, err
	}
	defer done()
	p, res, err := svc.playlists.Add(ctx, name, source, a.progress)
	if err != nil {
		return ImportResult{}, importError(err)
	}
	dto := toPlaylist(p)
	return ImportResult{Playlist: &dto, Imported: res.Imported, Skipped: res.Skipped}, nil
}

// RefreshPlaylist re-imports a playlist. Without force, URL playlists are
// rate limited and TooSoonMinutes is set instead of returning an error.
func (a *App) RefreshPlaylist(id int64, force bool) (ImportResult, error) {
	svc, err := a.ready()
	if err != nil {
		return ImportResult{}, err
	}
	ctx, done, err := a.beginImport()
	if err != nil {
		return ImportResult{}, err
	}
	defer done()
	res, err := svc.playlists.Refresh(ctx, id, force, a.progress)
	var tooSoon *playlist.TooSoonError
	if errors.As(err, &tooSoon) {
		return ImportResult{TooSoonMinutes: tooSoon.Minutes()}, nil
	}
	if err != nil {
		return ImportResult{Skipped: res.Skipped}, importError(err)
	}
	p, err := svc.playlists.Get(a.ctx, id)
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
	svc, err := a.ready()
	if err != nil {
		return err
	}
	return svc.playlists.Rename(a.ctx, id, name)
}

// DeletePlaylist removes a playlist and its favorites.
func (a *App) DeletePlaylist(id int64) error {
	svc, err := a.ready()
	if err != nil {
		return err
	}
	return svc.playlists.Delete(a.ctx, id)
}

// --- channels ---

// ListGroups returns a playlist's groups in playlist order.
func (a *App) ListGroups(playlistID int64) ([]Group, error) {
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	gs, err := svc.channels.Groups(a.ctx, playlistID)
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
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	f := channel.Filter{PlaylistID: q.PlaylistID, FavoritesOnly: q.FavoritesOnly, Query: q.Search}
	if !q.AllGroups {
		f.Group = &q.Group
	}
	return svc.channels.IDs(a.ctx, f)
}

// Channels returns the rows for ids, in the order given.
func (a *App) Channels(ids []int64) ([]Channel, error) {
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	cs, err := svc.channels.ByIDs(a.ctx, ids)
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
	svc, err := a.ready()
	if err != nil {
		return err
	}
	return svc.channels.SetFavorite(a.ctx, channelID, favorite)
}

// Play opens a channel in the configured player and returns its name.
func (a *App) Play(channelID int64) (string, error) {
	svc, err := a.ready()
	if err != nil {
		return "", err
	}
	return svc.channels.Play(a.ctx, channelID)
}

// --- settings ---

// GetSettings returns the current settings.
func (a *App) GetSettings() (Settings, error) {
	svc, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	ls, err := svc.settings.Get(a.ctx)
	if err != nil {
		return Settings{}, err
	}
	s := Settings{
		PlayerPath: ls.PlayerPath, PlayerName: ls.PlayerName, PlayerOverride: ls.PlayerOverride,
		UserAgent: ls.UserAgent, DefaultAgent: fetch.DefaultUserAgent, Version: version,
	}
	if ls.PlayerError != nil {
		s.PlayerError = ls.PlayerError.Error()
	}
	if p, err := database.ResolvePath(); err == nil {
		s.DataDir = filepath.Dir(p)
	}
	return s, nil
}

// ChoosePlayer asks the user to locate a supported player and saves it.
func (a *App) ChoosePlayer() (Settings, error) {
	svc, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	opts := runtime.OpenDialogOptions{Title: "Choose a player (VLC, mpv, MPC-HC, PotPlayer or IINA)"}
	if goruntime.GOOS == "windows" {
		opts.Filters = []runtime.FileFilter{{
			DisplayName: "Supported players",
			Pattern:     strings.Join(player.WindowsExecutables(), ";"),
		}}
	}
	path, err := runtime.OpenFileDialog(a.ctx, opts)
	if err != nil {
		return Settings{}, err
	}
	if path != "" {
		if err := svc.settings.SetPlayerPath(a.ctx, path); err != nil {
			return Settings{}, err
		}
	}
	return a.GetSettings()
}

// ResetPlayer returns to automatic VLC detection.
func (a *App) ResetPlayer() (Settings, error) {
	svc, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	if err := svc.settings.SetPlayerPath(a.ctx, ""); err != nil {
		return Settings{}, err
	}
	return a.GetSettings()
}

// SetUserAgent sets the User-Agent for playlist downloads ("" = default).
func (a *App) SetUserAgent(ua string) error {
	svc, err := a.ready()
	if err != nil {
		return err
	}
	return svc.settings.SetUserAgent(a.ctx, ua)
}

// --- helpers ---

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

func toPlaylist(p playlist.Playlist) Playlist {
	src := p.Source
	if p.SourceType == playlist.SourceURL {
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

// importError rewrites add/refresh errors that would otherwise read poorly
// in the UI.
func importError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return errors.New("import cancelled; the previous channel list was kept")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("import timed out; the previous channel list was kept")
	case errors.Is(err, fetch.ErrTooLarge):
		return fmt.Errorf("%w (limit %d MB)", err, fetch.DefaultMaxBytes>>20)
	}
	return err
}
