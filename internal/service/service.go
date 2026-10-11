// Package service holds the application's business logic: importing and
// refreshing playlists, browsing channels, favorites, playback and
// settings. It is the single API used by both the GUI and the debug CLI;
// persistence goes through the repository package.
package service

import (
	"time"

	"iptv-client/internal/repository"
)

// Services groups the application services.
type Services struct {
	Playlists *PlaylistService
	Channels  *ChannelService
	Settings  *SettingsService
}

// New returns the services backed by db, with default settings.
func New(db *repository.Database) *Services {
	settings := &SettingsService{db: db}
	return &Services{
		Playlists: &PlaylistService{db: db, MinRefreshInterval: DefaultMinRefreshInterval, Now: time.Now},
		Channels:  &ChannelService{db: db, settings: settings, Now: time.Now},
		Settings:  settings,
	}
}
