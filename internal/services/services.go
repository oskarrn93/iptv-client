// Package services wires the domain services together, so the app and the
// debug CLI run the same ones.
package services

import (
	"iptv-client/internal/channel"
	"iptv-client/internal/database"
	"iptv-client/internal/playlist"
	"iptv-client/internal/setting"
)

// Services are the domain services over one database.
type Services struct {
	Playlists *playlist.Service
	Channels  *channel.Service
	Settings  *setting.Service
}

// New returns the services backed by db.
func New(db *database.DB) *Services {
	settings := setting.NewService(db)
	return &Services{
		Playlists: playlist.NewService(db, settings),
		Channels:  channel.NewService(db, settings),
		Settings:  settings,
	}
}
