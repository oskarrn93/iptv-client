// Package setting stores and validates the user's settings: the media
// player location, the preferred audio languages, the User-Agent used to
// download playlists, the log level and the theme.
package setting

import (
	"cmp"
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
	// Normalized by player.NormalizeLanguages, e.g. "sv,en".
	KeyAudioLanguages = "audio_languages"
	KeyTheme          = "theme"
)

// Themes. ThemeNative follows the operating system.
const (
	ThemeNative = "native"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// DefaultAudioLanguages applies until the user sets their own. Players fall
// back to the stream's default track when none matches.
const DefaultAudioLanguages = "en"

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
	AudioLanguages string // e.g. "sv,en"; never ""
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
	langs, err := s.AudioLanguages(ctx)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{PlayerOverride: override != "", UserAgent: ua, AudioLanguages: langs}
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

// AudioLanguages returns the preferred audio languages in order, e.g.
// "sv,en".
func (s *Service) AudioLanguages(ctx context.Context) (string, error) {
	langs, err := s.repo.Get(ctx, KeyAudioLanguages)
	return cmp.Or(langs, DefaultAudioLanguages), err
}

// SetAudioLanguages stores the preferred audio languages: language codes
// separated by commas or spaces, most preferred first ("" =
// DefaultAudioLanguages).
func (s *Service) SetAudioLanguages(ctx context.Context, langs string) error {
	langs, err := player.NormalizeLanguages(langs)
	if err != nil {
		return err
	}
	if langs == "" || langs == DefaultAudioLanguages {
		return s.repo.Delete(ctx, KeyAudioLanguages)
	}
	return s.repo.Set(ctx, KeyAudioLanguages, langs)
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

// SetLogLevel saves a log level. Debug is accepted but not saved, so it
// lasts until the app restarts and a forgotten Debug cannot fill the disk;
// the previously saved level is kept.
func (s *Service) SetLogLevel(ctx context.Context, level slog.Level) error {
	switch {
	case level < slog.LevelInfo:
		return nil
	case level == slog.LevelInfo:
		return s.repo.Delete(ctx, KeyLogLevel)
	}
	return s.repo.Set(ctx, KeyLogLevel, logging.LevelName(level))
}

// Theme returns the theme: ThemeNative, ThemeLight or ThemeDark.
func (s *Service) Theme(ctx context.Context) (string, error) {
	v, err := s.repo.Get(ctx, KeyTheme)
	if err != nil || (v != ThemeLight && v != ThemeDark) {
		return ThemeNative, err
	}
	return v, nil
}

// SetTheme saves the theme: ThemeNative, ThemeLight or ThemeDark.
func (s *Service) SetTheme(ctx context.Context, theme string) error {
	switch theme {
	case ThemeNative:
		return s.repo.Delete(ctx, KeyTheme)
	case ThemeLight, ThemeDark:
		return s.repo.Set(ctx, KeyTheme, theme)
	}
	return errors.New("invalid theme")
}
