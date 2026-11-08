package playlist

import (
	"context"
	"time"
)

// SyncState remembers failed background refreshes between calls to Sync. A
// failed refresh is not marked as done, so without it a failing playlist or
// guide would be downloaded again at every sync, and a big guide is
// hundreds of MB. The zero value is ready to use.
type SyncState struct {
	failed map[syncItem]time.Time
}

type syncItem struct {
	id    int64
	guide bool
}

// SyncStep refreshes the channels of playlist id or, with guide set, its
// program guide. stop ends the sync (the user cancelled, or is importing);
// err is a failed refresh.
type SyncStep func(id int64, guide bool) (stop bool, err error)

// Sync refreshes, with step, the channels and guides that are due (Due,
// GuideDue): one at a time, in the user's order, a playlist's channels
// before its guide. An item that failed is retried once it would be stale
// again.
func (s *Service) Sync(ctx context.Context, st *SyncState, step SyncStep) error {
	ps, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	if st.failed == nil {
		st.failed = map[syncItem]time.Time{}
	}
	now := s.Now()
	due := func(it syncItem, stale bool, maxAge time.Duration) bool {
		return stale && now.Sub(st.failed[it]) >= maxAge
	}
	// run reports whether the sync should stop.
	run := func(it syncItem) bool {
		stop, err := step(it.id, it.guide)
		switch {
		case stop:
		case err != nil:
			st.failed[it] = s.Now()
		default:
			delete(st.failed, it)
		}
		return stop
	}
	for _, p := range ps {
		it := syncItem{id: p.ID}
		if due(it, p.Due(now), MaxAge) {
			if run(it) {
				return nil
			}
			// The refresh may have named a new guide.
			if p, err = s.repo.Get(ctx, p.ID); err != nil {
				continue
			}
		}
		it.guide = true
		if due(it, p.GuideDue(now), GuideMaxAge) && run(it) {
			return nil
		}
	}
	return nil
}
