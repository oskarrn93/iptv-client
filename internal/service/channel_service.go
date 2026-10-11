package service

import (
	"context"
	"time"

	"iptv-client/internal/model"
	"iptv-client/internal/player"
	"iptv-client/internal/repository"
)

// ChannelService browses channels, manages favorites and starts playback.
type ChannelService struct {
	db       *repository.Database
	settings *SettingsService
	Now      func() time.Time
}

// Groups returns the groups of a playlist in playlist order, with counts.
func (s *ChannelService) Groups(ctx context.Context, playlistID int64) ([]model.Group, error) {
	return s.db.Channels.ListGroups(ctx, playlistID)
}

// IDs returns the IDs of all channels matching f, in display order.
func (s *ChannelService) IDs(ctx context.Context, f model.ChannelFilter) ([]int64, error) {
	return s.db.Channels.ListIDs(ctx, f)
}

// ByIDs returns the list fields of the channels with the given IDs, in the
// order given. Unknown IDs are skipped.
func (s *ChannelService) ByIDs(ctx context.Context, ids []int64) ([]model.Channel, error) {
	return s.db.Channels.ListByIDs(ctx, ids)
}

// Get returns one channel with all its fields.
func (s *ChannelService) Get(ctx context.Context, id int64) (model.Channel, error) {
	return s.db.Channels.Get(ctx, id)
}

// SetFavorite marks or unmarks a channel as favorite.
func (s *ChannelService) SetFavorite(ctx context.Context, channelID int64, favorite bool) error {
	playlistID, key, err := s.db.Channels.FavoriteKey(ctx, channelID)
	if err != nil {
		return err
	}
	if favorite {
		return s.db.Favorites.Add(ctx, playlistID, key, s.Now())
	}
	return s.db.Favorites.Remove(ctx, playlistID, key)
}

// Play launches VLC for a channel.
func (s *ChannelService) Play(ctx context.Context, channelID int64) error {
	ch, err := s.db.Channels.Get(ctx, channelID)
	if err != nil {
		return err
	}
	vlc, err := s.settings.VLCPath(ctx)
	if err != nil {
		return err
	}
	// Launch re-validates the URL and options; stored data is not trusted.
	return player.Launch(vlc, ch.Name, ch.URL, ch.Options)
}
