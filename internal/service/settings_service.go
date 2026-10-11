package service

import (
	"context"
	"errors"
	"strings"

	"iptv-client/internal/player"
	"iptv-client/internal/repository"
)

// Setting keys.
const (
	SettingVLCPath   = "vlc_path"
	SettingUserAgent = "user_agent"
)

// SettingsService reads and validates the user's settings.
type SettingsService struct {
	db *repository.Database
}

// Settings is the user-facing configuration with VLC resolved.
type Settings struct {
	VLCPath     string // "" if VLC was not found
	VLCOverride bool   // VLCPath was chosen by the user
	VLCError    error  // why VLC was not found
	UserAgent   string // "" = fetch.DefaultUserAgent
}

// Get returns the current settings.
func (s *SettingsService) Get(ctx context.Context) (Settings, error) {
	override, err := s.db.Settings.Get(ctx, SettingVLCPath)
	if err != nil {
		return Settings{}, err
	}
	ua, err := s.db.Settings.Get(ctx, SettingUserAgent)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{VLCOverride: override != "", UserAgent: ua}
	out.VLCPath, out.VLCError = player.Find(override)
	return out, nil
}

// VLCPath returns the configured VLC path, or the auto-detected one.
func (s *SettingsService) VLCPath(ctx context.Context) (string, error) {
	override, err := s.db.Settings.Get(ctx, SettingVLCPath)
	if err != nil {
		return "", err
	}
	return player.Find(override)
}

// SetVLCPath stores a VLC location chosen by the user ("" = auto-detect).
// A macOS VLC.app bundle is accepted. Only call this with a path the user
// picked in a native file dialog.
func (s *SettingsService) SetVLCPath(ctx context.Context, path string) error {
	if path == "" {
		return s.db.Settings.Delete(ctx, SettingVLCPath)
	}
	path, err := player.ResolveExecutable(path)
	if err != nil {
		return err
	}
	return s.db.Settings.Set(ctx, SettingVLCPath, path)
}

// SetUserAgent stores the User-Agent used to download playlists
// ("" = fetch.DefaultUserAgent).
func (s *SettingsService) SetUserAgent(ctx context.Context, ua string) error {
	ua = strings.TrimSpace(ua)
	if strings.ContainsAny(ua, "\r\n") || len(ua) > 512 {
		return errors.New("invalid User-Agent")
	}
	if ua == "" {
		return s.db.Settings.Delete(ctx, SettingUserAgent)
	}
	return s.db.Settings.Set(ctx, SettingUserAgent, ua)
}
