// Package setting stores and validates the user's settings: the media
// player location, the User-Agent used to download playlists and the log
// level.
package setting

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"iptv-client/internal/database"
	"iptv-client/internal/logging"
	"iptv-client/internal/player"
)

// Setting keys.
const (
	KeyPlayerPath = "vlc_path" // named before players other than VLC were supported
	KeyUserAgent  = "user_agent"
	KeyLogLevel   = "log_level"
)

// Service reads and validates the user's settings.
type Service struct {
	repo *Repository
}

// NewService returns a Service backed by db.
func NewService(db *database.DB) *Service {
	return &Service{repo: NewRepository(db)}
}

// Settings is the user-facing configuration with the player resolved.
type Settings struct {
	PlayerPath     string // "" if no player was found
	PlayerName     string // e.g. "VLC"; "" if no player was found
	PlayerOverride bool   // PlayerPath was chosen by the user
	PlayerError    error  // why no player was found
	UserAgent      string // "" = fetch.DefaultUserAgent
}

// Get returns the current settings.
func (s *Service) Get(ctx context.Context) (Settings, error) {
	override, err := s.repo.Get(ctx, KeyPlayerPath)
	if err != nil {
		return Settings{}, err
	}
	ua, err := s.repo.Get(ctx, KeyUserAgent)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{PlayerOverride: override != "", UserAgent: ua}
	out.PlayerPath, out.PlayerError = player.Find(override)
	if p, err := player.ForExecutable(out.PlayerPath); err == nil {
		out.PlayerName = p.Name()
	}
	return out, nil
}

// PlayerPath returns the chosen player's path, or the auto-detected VLC.
func (s *Service) PlayerPath(ctx context.Context) (string, error) {
	override, err := s.repo.Get(ctx, KeyPlayerPath)
	if err != nil {
		return "", err
	}
	return player.Find(override)
}

// SetPlayerPath stores a player location chosen by the user ("" =
// auto-detect VLC). A macOS app bundle is accepted. Only call this with a
// path the user picked in a native file dialog.
func (s *Service) SetPlayerPath(ctx context.Context, path string) error {
	if path == "" {
		return s.repo.Delete(ctx, KeyPlayerPath)
	}
	path, err := player.ResolveExecutable(path)
	if err != nil {
		return err
	}
	return s.repo.Set(ctx, KeyPlayerPath, path)
}

// UserAgent returns the User-Agent used to download playlists
// ("" = fetch.DefaultUserAgent).
func (s *Service) UserAgent(ctx context.Context) (string, error) {
	return s.repo.Get(ctx, KeyUserAgent)
}

// SetUserAgent stores the User-Agent used to download playlists
// ("" = fetch.DefaultUserAgent).
func (s *Service) SetUserAgent(ctx context.Context, ua string) error {
	ua = strings.TrimSpace(ua)
	if strings.ContainsAny(ua, "\r\n") || len(ua) > 512 {
		return errors.New("invalid User-Agent")
	}
	if ua == "" {
		return s.repo.Delete(ctx, KeyUserAgent)
	}
	return s.repo.Set(ctx, KeyUserAgent, ua)
}

// LogLevel returns the saved log level (Info if unset).
func (s *Service) LogLevel(ctx context.Context) (slog.Level, error) {
	v, err := s.repo.Get(ctx, KeyLogLevel)
	if err != nil || v == "" {
		return slog.LevelInfo, err
	}
	l, err := logging.ParseLevel(v)
	if err != nil {
		return slog.LevelInfo, nil // ignore a corrupt value
	}
	return l, nil
}

// SetLogLevel saves a log level name. Debug is accepted but not saved, so
// it lasts until the app restarts and a forgotten Debug cannot fill the
// disk; the previously saved level is kept.
func (s *Service) SetLogLevel(ctx context.Context, level string) error {
	if _, err := logging.ParseLevel(level); err != nil {
		return err
	}
	switch level {
	case logging.LevelDebug:
		return nil
	case logging.LevelInfo:
		return s.repo.Delete(ctx, KeyLogLevel)
	}
	return s.repo.Set(ctx, KeyLogLevel, level)
}
