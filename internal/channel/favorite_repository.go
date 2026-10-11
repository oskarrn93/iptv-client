package channel

import (
	"context"
	"time"

	"iptv-client/internal/database"
)

// favoriteRepository reads and writes the favorites table. Favorites are
// keyed on (playlist, favorite key) rather than channel ID, so they survive
// refreshes; see Repository.FavoriteKey.
type favoriteRepository struct {
	q database.Querier
}

// Add marks a key as favorite. Adding an existing favorite is a no-op.
func (r *favoriteRepository) Add(ctx context.Context, playlistID int64, key string, createdAt time.Time) error {
	_, err := r.q.ExecContext(ctx, `
		INSERT OR IGNORE INTO favorites (playlist_id, fav_key, created_at)
		VALUES (?, ?, ?)`, playlistID, key, createdAt.Unix())
	return err
}

// Remove unmarks a key. Removing a missing favorite is a no-op.
func (r *favoriteRepository) Remove(ctx context.Context, playlistID int64, key string) error {
	_, err := r.q.ExecContext(ctx, `
		DELETE FROM favorites
		WHERE playlist_id = ? AND fav_key = ?`, playlistID, key)
	return err
}
