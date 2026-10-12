// Package playlist adds, refreshes, renames and deletes playlist sources. A
// refresh downloads or reads the source, parses it and replaces the
// playlist's channels (stored by the channel package) in one transaction.
package playlist

import (
	"time"

	"iptv-client/internal/fetch"
)

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

// DisplaySource is the source as it may be shown: a URL is redacted, since
// it often carries account credentials.
func (p Playlist) DisplaySource() string {
	if p.SourceType == SourceURL {
		return fetch.Redact(p.Source)
	}
	return p.Source
}
