// Package channel browses a playlist's channels, manages favorites and
// starts playback. It also stores the channels a playlist import produces;
// see Repository.ReplaceAllInPlaylist.
package channel

import "iptv-client/internal/epg"

// Channel is one playable entry. Repository.ListByIDs leaves out URL and
// Options; Repository.Get fills all fields.
type Channel struct {
	ID         int64
	PlaylistID int64
	Name       string
	URL        string
	Group      string
	TvgID      string
	TvgName    string
	TvgLogo    string
	Options    map[string]string
	Favorite   bool
}

// GuideRef returns the fields the program guide matches the channel on.
func (c Channel) GuideRef() epg.Ref {
	return epg.Ref{ChannelID: c.ID, PlaylistID: c.PlaylistID, TvgID: c.TvgID, TvgName: c.TvgName, Name: c.Name}
}

// Input is a channel to insert during a playlist refresh.
type Input struct {
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

// Filter selects channels. Zero values mean "no filter".
type Filter struct {
	PlaylistID    int64
	Group         *string // nil = all groups; "" = ungrouped
	FavoritesOnly bool
	Query         string // full-text search
}
