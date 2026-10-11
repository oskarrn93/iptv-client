// Package setting stores and validates the user's settings: the VLC
// location and the User-Agent used to download playlists.
package setting

import (
	"context"
	"errors"
	"strings"

	"iptv-client/internal/database"
	"iptv-client/internal/player"
)

// Setting keys.
const (
	KeyVLCPath   = "vlc_path"
	KeyUserAgent = "user_agent"
)

// Service reads and validates the user's settings.
type Service struct {
	repo *Repository
}

// NewService returns a Service backed by db.
func NewService(db *database.DB) *Service {
	return &Service{repo: NewRepository(db)}
}

// Settings is the user-facing configuration with VLC resolved.
type Settings struct {
	VLCPath     string // "" if VLC was not found
	VLCOverride bool   // VLCPath was chosen by the user
	VLCError    error  // why VLC was not found
	UserAgent   string // "" = fetch.DefaultUserAgent
}

// Get returns the current settings.
func (s *Service) Get(ctx context.Context) (Settings, error) {
	override, err := s.repo.Get(ctx, KeyVLCPath)
	if err != nil {
		return Settings{}, err
	}
	ua, err := s.repo.Get(ctx, KeyUserAgent)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{VLCOverride: override != "", UserAgent: ua}
	out.VLCPath, out.VLCError = player.Find(override)
	return out, nil
}

// VLCPath returns the configured VLC path, or the auto-detected one.
func (s *Service) VLCPath(ctx context.Context) (string, error) {
	override, err := s.repo.Get(ctx, KeyVLCPath)
	if err != nil {
		return "", err
	}
	return player.Find(override)
}

// SetVLCPath stores a VLC location chosen by the user ("" = auto-detect).
// A macOS VLC.app bundle is accepted. Only call this with a path the user
// picked in a native file dialog.
func (s *Service) SetVLCPath(ctx context.Context, path string) error {
	if path == "" {
		return s.repo.Delete(ctx, KeyVLCPath)
	}
	path, err := player.ResolveExecutable(path)
	if err != nil {
		return err
	}
	return s.repo.Set(ctx, KeyVLCPath, path)
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
