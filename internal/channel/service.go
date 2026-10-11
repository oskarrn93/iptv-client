package channel

import (
	"context"
	"time"

	"iptv-client/internal/database"
	"iptv-client/internal/player"
	"iptv-client/internal/setting"
)

// Service browses channels, manages favorites and starts playback.
type Service struct {
	repo      *Repository
	favorites *favoriteRepository
	settings  *setting.Service
	Now       func() time.Time
}

// NewService returns a Service backed by db that reads the VLC location
// from settings.
func NewService(db *database.DB, settings *setting.Service) *Service {
	return &Service{
		repo:      NewRepository(db),
		favorites: &favoriteRepository{q: db},
		settings:  settings,
		Now:       time.Now,
	}
}

// Groups returns the groups of a playlist in playlist order, with counts.
func (s *Service) Groups(ctx context.Context, playlistID int64) ([]Group, error) {
	return s.repo.ListGroups(ctx, playlistID)
}

// IDs returns the IDs of all channels matching f, in display order.
func (s *Service) IDs(ctx context.Context, f Filter) ([]int64, error) {
	return s.repo.ListIDs(ctx, f)
}

// ByIDs returns the list fields of the channels with the given IDs, in the
// order given. Unknown IDs are skipped.
func (s *Service) ByIDs(ctx context.Context, ids []int64) ([]Channel, error) {
	return s.repo.ListByIDs(ctx, ids)
}

// Get returns one channel with all its fields.
func (s *Service) Get(ctx context.Context, id int64) (Channel, error) {
	return s.repo.Get(ctx, id)
}

// SetFavorite marks or unmarks a channel as favorite.
func (s *Service) SetFavorite(ctx context.Context, channelID int64, favorite bool) error {
	playlistID, key, err := s.repo.FavoriteKey(ctx, channelID)
	if err != nil {
		return err
	}
	if favorite {
		return s.favorites.Add(ctx, playlistID, key, s.Now())
	}
	return s.favorites.Remove(ctx, playlistID, key)
}

// Play launches VLC for a channel.
func (s *Service) Play(ctx context.Context, channelID int64) error {
	ch, err := s.repo.Get(ctx, channelID)
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
