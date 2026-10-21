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
	// GuideURL is the program guide (XMLTV) URL set by the user; TvgURL is
	// the one named in the playlist header. See GuideSource.
	GuideURL         string
	TvgURL           string
	GuideRefreshedAt time.Time // zero if the guide was never imported
	Disabled         bool      // hidden from the playlist list
}

// GuideSource is the program guide URL in use, "" if there is none.
func (p Playlist) GuideSource() string {
	if p.GuideURL != "" {
		return p.GuideURL
	}
	return p.TvgURL
}

// DisplaySource is the source as it may be shown: a URL is redacted, since
// it often carries account credentials.
func (p Playlist) DisplaySource() string {
	if p.SourceType == SourceURL {
		return fetch.Redact(p.Source)
	}
	return p.Source
}

// DisplayGuideSource is GuideSource redacted for display, "" if there is none.
func (p Playlist) DisplayGuideSource() string {
	if src := p.GuideSource(); src != "" {
		return fetch.Redact(src)
	}
	return ""
}
