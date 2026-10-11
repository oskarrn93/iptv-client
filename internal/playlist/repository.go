package playlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"iptv-client/internal/database"
)

// Repository reads and writes the playlists table.
type Repository struct {
	q database.Querier
}

// NewRepository returns a Repository that runs its statements on q.
func NewRepository(q database.Querier) *Repository { return &Repository{q: q} }

// Create inserts a playlist (with no channels yet) and returns its ID.
func (r *Repository) Create(ctx context.Context, name string, t SourceType, source string, createdAt time.Time) (int64, error) {
	if t != SourceURL && t != SourceFile {
		return 0, fmt.Errorf("invalid source type %q", t)
	}
	res, err := r.q.ExecContext(ctx, `
		INSERT INTO playlists (name, source_type, source, created_at)
		VALUES (?, ?, ?, ?)`,
		name, string(t), source, createdAt.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get returns one playlist.
func (r *Repository) Get(ctx context.Context, id int64) (Playlist, error) {
	row := r.q.QueryRowContext(ctx, `
		SELECT id, name, source_type, source, etag, last_modified, last_refreshed_at, channel_count, created_at
		FROM playlists
		WHERE id = ?`, id)
	p, err := scanPlaylist(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, fmt.Errorf("playlist %d: %w", id, database.ErrNotFound)
	}
	return p, err
}

// Exists reports whether a playlist with this ID exists.
func (r *Repository) Exists(ctx context.Context, id int64) (bool, error) {
	var exists bool
	err := r.q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM playlists WHERE id = ?)`, id).Scan(&exists)
	return exists, err
}

// List returns all playlists ordered by name.
func (r *Repository) List(ctx context.Context) ([]Playlist, error) {
	rows, err := r.q.QueryContext(ctx, `
		SELECT id, name, source_type, source, etag, last_modified, last_refreshed_at, channel_count, created_at
		FROM playlists
		ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		p, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Rename changes a playlist's display name.
func (r *Repository) Rename(ctx context.Context, id int64, name string) error {
	return database.ExpectOne(r.q.ExecContext(ctx, `
		UPDATE playlists
		SET name = ?
		WHERE id = ?`, name, id))
}

// Delete removes a playlist row. Its channels and favorites are removed by
// ON DELETE CASCADE, but the search index is not: call
// channel.Repository.DeleteAllInPlaylist first, in the same transaction.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	return database.ExpectOne(r.q.ExecContext(ctx, `
		DELETE FROM playlists
		WHERE id = ?`, id))
}

// MarkRefreshed records a successful refresh that returned no new data.
func (r *Repository) MarkRefreshed(ctx context.Context, id int64, refreshedAt time.Time) error {
	return database.ExpectOne(r.q.ExecContext(ctx, `
		UPDATE playlists
		SET last_refreshed_at = ?
		WHERE id = ?`, refreshedAt.Unix(), id))
}

// SaveRefresh records a successful refresh that replaced the channels.
func (r *Repository) SaveRefresh(ctx context.Context, id int64, channelCount int, etag, lastModified string, refreshedAt time.Time) error {
	return database.ExpectOne(r.q.ExecContext(ctx, `
		UPDATE playlists
		SET channel_count = ?, etag = ?, last_modified = ?, last_refreshed_at = ?
		WHERE id = ?`, channelCount, etag, lastModified, refreshedAt.Unix(), id))
}

// scanPlaylist reads the columns selected by Get and List, in that order.
func scanPlaylist(row interface{ Scan(...any) error }) (Playlist, error) {
	var p Playlist
	var refreshed, created int64
	err := row.Scan(&p.ID, &p.Name, &p.SourceType, &p.Source, &p.ETag, &p.LastModified, &refreshed, &p.ChannelCount, &created)
	if err != nil {
		return Playlist{}, err
	}
	if refreshed > 0 {
		p.LastRefreshedAt = time.Unix(refreshed, 0)
	}
	p.CreatedAt = time.Unix(created, 0)
	return p, nil
}
