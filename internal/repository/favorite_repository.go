package repository

import (
	"context"
	"time"
)

// FavoriteRepository reads and writes the favorites table. Favorites are
// keyed on (playlist, favorite key) rather than channel ID, so they survive
// refreshes; see ChannelRepository.FavoriteKey.
type FavoriteRepository struct {
	q Querier
}

// Add marks a key as favorite. Adding an existing favorite is a no-op.
func (r *FavoriteRepository) Add(ctx context.Context, playlistID int64, key string, createdAt time.Time) error {
	_, err := r.q.ExecContext(ctx, `
		INSERT OR IGNORE INTO favorites (playlist_id, fav_key, created_at)
		VALUES (?, ?, ?)`, playlistID, key, createdAt.Unix())
	return err
}

// Remove unmarks a key. Removing a missing favorite is a no-op.
func (r *FavoriteRepository) Remove(ctx context.Context, playlistID int64, key string) error {
	_, err := r.q.ExecContext(ctx, `
		DELETE FROM favorites
		WHERE playlist_id = ? AND fav_key = ?`, playlistID, key)
	return err
}
