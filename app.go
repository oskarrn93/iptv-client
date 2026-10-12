package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"iptv-client/internal/channel"
	"iptv-client/internal/database"
	"iptv-client/internal/desktop"
	"iptv-client/internal/fetch"
	"iptv-client/internal/logging"
	"iptv-client/internal/player"
	"iptv-client/internal/playlist"
	"iptv-client/internal/services"
	"iptv-client/internal/update"
)

// App is bound to the frontend. Every exported method is callable from
// JavaScript, so methods accept only IDs and plain values: file system
// paths are chosen through native dialogs opened from Go, never passed in
// from the UI (C3 in the design review).
type App struct {
	ctx     context.Context
	db      *database.DB
	svc     *services.Services
	initErr error

	log      *slog.Logger
	logLevel *slog.LevelVar // the log level, changed by SetLogLevel
	logDir   string
	uiLog    *logging.UI
	imports  importGuard
	updater  *update.Updater
}

// NewApp creates the application. It logs to log, whose level is
// logLevel, and logDir is the folder OpenLogFolder shows.
func NewApp(log *slog.Logger, logLevel *slog.LevelVar, logDir string) *App {
	return &App{
		log:      log,
		logLevel: logLevel,
		logDir:   logDir,
		uiLog:    logging.NewUI(log),
		updater:  &update.Updater{Current: version, UserAgent: "iptv-client/" + version},
	}
}

// errNoDatabase means the database failed to open at startup.
var errNoDatabase = errors.New("could not open the database")

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.log.InfoContext(ctx, "starting", "version", version, "os", goruntime.GOOS, "arch", goruntime.GOARCH,
		"db_override", os.Getenv(database.PathEnv) != "")
	path, err := database.ResolvePath()
	if err == nil {
		var db *database.DB
		if db, err = database.Open(ctx, path); err == nil {
			a.db = db
			a.svc = services.New(db)
			a.started(ctx)
			return
		}
	}
	a.initErr = fmt.Errorf("%w: %w", errNoDatabase, err)
	a.log.ErrorContext(ctx, "cannot open the database", "err", err)
}

// started applies the saved log level and logs the player in use.
func (a *App) started(ctx context.Context) {
	if level, err := a.svc.Settings.LogLevel(ctx); err == nil {
		a.logLevel.Set(level)
	}
	a.log.InfoContext(ctx, "database open", "log_level", logging.LevelName(a.logLevel.Level()))
	// Finding the player reads the registry and searches folders; don't
	// hold up the window for a log line.
	go func() {
		if s, err := a.svc.Settings.Get(ctx); err == nil {
			a.log.InfoContext(ctx, "player", "name", s.PlayerName, "chosen", s.PlayerOverride, "found", s.PlayerError == nil)
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	a.log.InfoContext(ctx, "shutting down")
	if a.db != nil {
		a.db.Close()
	}
}

// ready returns the services or the startup error.
func (a *App) ready() (*services.Services, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	return a.svc, nil
}

// call starts a bound-method call: the returned context tags log lines
// with op and attrs, and end, deferred with the method's error, logs a
// failure (at Debug when it is part of normal use, otherwise at Warn).
func (a *App) call(op string, attrs ...slog.Attr) (ctx context.Context, end func(*error)) {
	ctx = logging.With(a.ctx, append([]slog.Attr{slog.String("op", op)}, attrs...)...)
	return ctx, func(err *error) {
		if *err == nil {
			return
		}
		level := slog.LevelWarn
		if expectedError(*err) {
			level = slog.LevelDebug
		}
		a.log.Log(ctx, level, "call failed", "err", *err)
	}
}

// expectedError reports errors that are part of normal use, or were
// already logged.
func expectedError(err error) bool {
	return errors.Is(err, errImportRunning) ||
		errors.Is(err, database.ErrNotFound) || errors.Is(err, errNoDatabase) ||
		errors.Is(err, context.Canceled)
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
	LogLevel       string `json:"logLevel"`       // debug, info, warn or error
	DataDir        string `json:"dataDir"`
	Version        string `json:"version"`
}

// UpdateInfo reports an update check.
type UpdateInfo struct {
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
}

// ImportProgressEvent is emitted while importing with the running count.
const ImportProgressEvent = "import-progress"

// UpdateProgressEvent is emitted while downloading an update with the
// percentage done.
const UpdateProgressEvent = "update-progress"

// --- playlists ---

// ListPlaylists returns all playlists.
func (a *App) ListPlaylists() (_ []Playlist, err error) {
	ctx, end := a.call("list_playlists")
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	ps, err := svc.Playlists.List(ctx)
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
	return a.add(name, url, playlist.SourceURL)
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
	return a.add(name, path, playlist.SourceFile)
}

func (a *App) add(name, source string, typ playlist.SourceType) (ImportResult, error) {
	return a.runImport("add_playlist", []slog.Attr{slog.String("source_type", string(typ))},
		func(ctx context.Context, svc *services.Services) (playlist.Playlist, playlist.RefreshResult, error) {
			return svc.Playlists.Add(ctx, name, source, a.progress)
		})
}

// RefreshPlaylist re-imports a playlist. Without force, URL playlists are
// rate limited and TooSoonMinutes is set instead of returning an error.
func (a *App) RefreshPlaylist(id int64, force bool) (ImportResult, error) {
	return a.runImport("refresh_playlist", []slog.Attr{slog.Int64("playlist_id", id), slog.Bool("force", force)},
		func(ctx context.Context, svc *services.Services) (playlist.Playlist, playlist.RefreshResult, error) {
			res, err := svc.Playlists.Refresh(ctx, id, force, a.progress)
			if err != nil {
				return playlist.Playlist{}, res, err
			}
			// The import has committed: a cancel arriving now must not fail it.
			p, err := svc.Playlists.Get(context.WithoutCancel(ctx), id)
			return p, res, err
		})
}

// runImport runs an add or refresh under the import guard, logs its start
// and result, and builds the result for the UI.
func (a *App) runImport(op string, attrs []slog.Attr,
	run func(context.Context, *services.Services) (playlist.Playlist, playlist.RefreshResult, error)) (_ ImportResult, err error) {

	ctx, end := a.call(op, attrs...)
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return ImportResult{}, err
	}
	ctx, done, err := a.imports.begin(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer done()
	start := time.Now()
	a.log.InfoContext(ctx, "import started")
	p, res, err := run(ctx, svc)
	var tooSoon *playlist.TooSoonError
	if errors.As(err, &tooSoon) {
		a.log.InfoContext(ctx, "refresh refused: too soon", "wait_minutes", tooSoon.Minutes())
		return ImportResult{TooSoonMinutes: tooSoon.Minutes()}, nil
	}
	if err != nil {
		return ImportResult{}, importError(err)
	}
	a.log.InfoContext(ctx, "import finished", "playlist_id", p.ID, "imported", res.Imported,
		"skipped", res.Skipped, "hls", res.HLS, "not_modified", res.NotModified,
		"took", time.Since(start).Round(time.Millisecond))
	dto := toPlaylist(p)
	return ImportResult{Playlist: &dto, Imported: res.Imported, Skipped: res.Skipped, NotModified: res.NotModified}, nil
}

// CancelImport stops a running add/refresh. The previous data is kept.
func (a *App) CancelImport() {
	a.log.InfoContext(a.ctx, "import cancel requested")
	a.imports.stop()
}

// RenamePlaylist changes a playlist's name.
func (a *App) RenamePlaylist(id int64, name string) (err error) {
	ctx, end := a.call("rename_playlist", slog.Int64("playlist_id", id))
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return err
	}
	return svc.Playlists.Rename(ctx, id, name)
}

// DeletePlaylist removes a playlist and its favorites.
func (a *App) DeletePlaylist(id int64) (err error) {
	ctx, end := a.call("delete_playlist", slog.Int64("playlist_id", id))
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return err
	}
	if err := svc.Playlists.Delete(ctx, id); err != nil {
		return err
	}
	a.log.InfoContext(ctx, "playlist deleted")
	return nil
}

// --- channels ---

// ListGroups returns a playlist's groups in playlist order.
func (a *App) ListGroups(playlistID int64) (_ []Group, err error) {
	ctx, end := a.call("list_groups", slog.Int64("playlist_id", playlistID))
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	gs, err := svc.Channels.Groups(ctx, playlistID)
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
func (a *App) ChannelIDs(q ChannelQuery) (_ []int64, err error) {
	ctx, end := a.call("channel_ids", slog.Int64("playlist_id", q.PlaylistID))
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	f := channel.Filter{PlaylistID: q.PlaylistID, FavoritesOnly: q.FavoritesOnly, Query: q.Search}
	if !q.AllGroups {
		f.Group = &q.Group
	}
	return svc.Channels.IDs(ctx, f)
}

// Channels returns the rows for ids, in the order given.
func (a *App) Channels(ids []int64) (_ []Channel, err error) {
	ctx, end := a.call("channels")
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return nil, err
	}
	cs, err := svc.Channels.ByIDs(ctx, ids)
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
func (a *App) SetFavorite(channelID int64, favorite bool) (err error) {
	ctx, end := a.call("set_favorite", slog.Int64("channel_id", channelID), slog.Bool("favorite", favorite))
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return err
	}
	return svc.Channels.SetFavorite(ctx, channelID, favorite)
}

// Play opens a channel in the configured player and returns its name.
func (a *App) Play(channelID int64) (_ string, err error) {
	ctx, end := a.call("play", slog.Int64("channel_id", channelID))
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return "", err
	}
	name, err := svc.Channels.Play(ctx, channelID)
	if err != nil {
		return "", err
	}
	a.log.InfoContext(ctx, "player started")
	return name, nil
}

// CopyStreamURL puts a channel's stream URL on the clipboard. The URL is
// copied from Go so it never reaches the UI: provider URLs often embed the
// account credentials.
func (a *App) CopyStreamURL(channelID int64) (err error) {
	ctx, end := a.call("copy_stream_url", slog.Int64("channel_id", channelID))
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return err
	}
	url, err := svc.Channels.StreamURL(ctx, channelID)
	if err != nil {
		return err
	}
	return runtime.ClipboardSetText(a.ctx, url)
}

// --- settings ---

// GetSettings returns the current settings.
func (a *App) GetSettings() (_ Settings, err error) {
	ctx, end := a.call("get_settings")
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	return a.settings(ctx, svc)
}

// settings builds the Settings DTO. Bound methods use it rather than
// calling GetSettings, which would log a failure twice under another op.
func (a *App) settings(ctx context.Context, svc *services.Services) (Settings, error) {
	ls, err := svc.Settings.Get(ctx)
	if err != nil {
		return Settings{}, err
	}
	s := Settings{
		PlayerPath: ls.PlayerPath, PlayerName: ls.PlayerName, PlayerOverride: ls.PlayerOverride,
		UserAgent: ls.UserAgent, DefaultAgent: fetch.DefaultUserAgent,
		LogLevel: logging.LevelName(a.logLevel.Level()), Version: version,
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
func (a *App) ChoosePlayer() (_ Settings, err error) {
	ctx, end := a.call("choose_player")
	defer end(&err)
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
		if err := svc.Settings.SetPlayerPath(ctx, path); err != nil {
			return Settings{}, err
		}
	}
	s, err := a.settings(ctx, svc)
	if err == nil && path != "" {
		a.log.InfoContext(ctx, "player chosen", "name", s.PlayerName)
	}
	return s, err
}

// ResetPlayer returns to automatic VLC detection.
func (a *App) ResetPlayer() (_ Settings, err error) {
	ctx, end := a.call("reset_player")
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return Settings{}, err
	}
	if err := svc.Settings.SetPlayerPath(ctx, ""); err != nil {
		return Settings{}, err
	}
	a.log.InfoContext(ctx, "player reset to auto-detect")
	return a.settings(ctx, svc)
}

// SetUserAgent sets the User-Agent for playlist downloads ("" = default).
func (a *App) SetUserAgent(ua string) (err error) {
	ctx, end := a.call("set_user_agent")
	defer end(&err)
	svc, err := a.ready()
	if err != nil {
		return err
	}
	if err := svc.Settings.SetUserAgent(ctx, ua); err != nil {
		return err
	}
	// Never the value: a custom User-Agent can identify the account.
	a.log.DebugContext(ctx, "user agent saved", "custom", strings.TrimSpace(ua) != "")
	return nil
}

// --- logging ---
// These work without the database too, when the logs matter most.

// SetLogLevel sets the log level: debug, info, warn or error. Debug lasts
// until the app restarts; the others are saved.
func (a *App) SetLogLevel(level string) (err error) {
	ctx, end := a.call("set_log_level")
	defer end(&err)
	l, err := logging.ParseLevel(level)
	if err != nil {
		return err
	}
	if svc, err := a.ready(); err == nil {
		if err := svc.Settings.SetLogLevel(ctx, l); err != nil {
			return err
		}
	}
	old := a.logLevel.Level()
	if l == old {
		return nil
	}
	a.logLevel.Set(l)
	// At least Info, so the change is visible whatever the new level.
	a.log.Log(ctx, max(l, slog.LevelInfo), "log level changed", "from", logging.LevelName(old), "to", level)
	return nil
}

// OpenLogFolder shows the log folder in Explorer or Finder.
func (a *App) OpenLogFolder() (err error) {
	ctx, end := a.call("open_log_folder")
	defer end(&err)
	dir := a.logDir
	if dir == "" {
		return errors.New("the log folder is unknown")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// The user may be about to attach these files: move any raw crash
	// output into the (redacted) log first.
	logging.RecoverCrashes(ctx, a.log, dir)
	return desktop.OpenFolder(dir)
}

// Log writes a frontend log line (component=ui). The input is untrusted:
// the level must be debug, info, warn or error, keys must be lower_snake
// case, values are size-limited and redacted, and lines are rate-limited.
// It never fails, so the UI never has to handle (or log) a logging error.
func (a *App) Log(level, msg string, attrs map[string]string) {
	a.uiLog.Log(level, msg, attrs)
}

// --- updates ---
// These do not need the database, so they also work when it failed to open.

// CheckForUpdate looks up the latest release. InstallUpdate installs it if
// it is newer than the running version.
func (a *App) CheckForUpdate() (_ UpdateInfo, err error) {
	ctx, end := a.call("check_update")
	defer end(&err)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	r, newer, err := a.updater.Check(ctx)
	if err != nil {
		return UpdateInfo{}, err
	}
	a.log.InfoContext(ctx, "update check", "latest", r.Version, "available", newer)
	return UpdateInfo{Latest: r.Version, Available: newer}, nil
}

// InstallUpdate downloads the release found by CheckForUpdate, starts its
// installation and quits the app, which restarts on the new version.
func (a *App) InstallUpdate() (err error) {
	ctx, end := a.call("install_update")
	defer end(&err)
	// Generous, so slow connections can still finish the download.
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	a.log.InfoContext(ctx, "installing update")
	err = a.updater.Install(ctx, func(pct int) {
		runtime.EventsEmit(a.ctx, UpdateProgressEvent, pct)
	})
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "update installer started; quitting")
	runtime.Quit(a.ctx)
	return nil
}

// --- helpers ---

func (a *App) progress(n int) { runtime.EventsEmit(a.ctx, ImportProgressEvent, n) }

func toPlaylist(p playlist.Playlist) Playlist {
	var refreshed int64
	if !p.LastRefreshedAt.IsZero() {
		refreshed = p.LastRefreshedAt.Unix()
	}
	return Playlist{
		ID: p.ID, Name: p.Name, SourceType: string(p.SourceType), Source: p.DisplaySource(),
		ChannelCount: p.ChannelCount, LastRefreshedAt: refreshed,
	}
}

// uiError replaces an error's message for the UI but keeps it for errors.Is.
type uiError struct {
	msg string
	err error
}

func (e uiError) Error() string { return e.msg }
func (e uiError) Unwrap() error { return e.err }

// importError rewrites add/refresh errors that would otherwise read poorly
// in the UI.
func importError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return uiError{"import cancelled; the previous channel list was kept", err}
	case errors.Is(err, context.DeadlineExceeded):
		return uiError{"import timed out; the previous channel list was kept", err}
	}
	return err
}
