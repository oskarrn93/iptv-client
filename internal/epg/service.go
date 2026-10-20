package epg

import (
	"context"
	"time"

	"iptv-client/internal/database"
)

// Service looks up what is on. Guides are imported by the playlist package,
// which owns the guide URL; see playlist.Service.RefreshGuide.
type Service struct {
	repo *Repository
	Now  func() time.Time
}

// NewService returns a Service backed by db.
func NewService(db *database.DB) *Service {
	return &Service{repo: NewRepository(db), Now: time.Now}
}

// Current returns, for each ref the guide matches, its icon and the
// programme on now (if any).
func (s *Service) Current(ctx context.Context, refs []Ref) (map[int64]Info, error) {
	return s.repo.Current(ctx, refs, s.Now())
}

// Upcoming returns a channel's icon and up to n programmes that have not
// ended. ok is false if the guide does not match the channel.
func (s *Service) Upcoming(ctx context.Context, ref Ref, n int) (Info, bool, error) {
	return s.repo.Upcoming(ctx, ref, s.Now(), n)
}
