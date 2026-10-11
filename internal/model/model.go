// Package model holds the entities shared by the repository and service
// layers.
package model

import "time"

// SourceType says where a playlist is loaded from.
type SourceType string

const (
	SourceURL  SourceType = "url"
	SourceFile SourceType = "file"
)

// Playlist is a stored playlist source.
type Playlist struct {
	ID              int64
	Name            string
	SourceType      SourceType
	Source          string
	ETag            string
	LastModified    string
	LastRefreshedAt time.Time // zero if never refreshed successfully
	ChannelCount    int
	CreatedAt       time.Time
}

// Channel is one playable entry. ChannelRepository.ListByIDs fills only the
// list fields (ID, Name, Group, Favorite); ChannelRepository.Get fills all
// of them.
type Channel struct {
	ID       int64
	Name     string
	URL      string
	Group    string
	TvgID    string
	Options  map[string]string
	Favorite bool
}

// ChannelInput is a channel to insert during a refresh.
type ChannelInput struct {
	Name    string
	URL     string
	Group   string
	TvgID   string
	TvgName string
	TvgLogo string
	Options map[string]string
}

// Group is a group within a playlist with its channel count.
type Group struct {
	Name  string
	Count int
}

// ChannelFilter selects channels. Zero values mean "no filter".
type ChannelFilter struct {
	PlaylistID    int64
	Group         *string // nil = all groups; "" = ungrouped
	FavoritesOnly bool
	Query         string // full-text search
}
