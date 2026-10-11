package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"iptv-client/internal/model"
)

// ChannelRepository reads and writes the channels table and its full-text
// index channels_fts.
type ChannelRepository struct {
	q Querier
}

// ListGroups returns the groups of a playlist in playlist order, with counts.
func (r *ChannelRepository) ListGroups(ctx context.Context, playlistID int64) ([]model.Group, error) {
	rows, err := r.q.QueryContext(ctx, `
		SELECT group_title, COUNT(*)
		FROM channels
		WHERE playlist_id = ?
		GROUP BY group_title
		ORDER BY MIN(position)`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.Name, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListIDs returns the IDs of all channels matching f, in display order.
// The UI fetches this list once and then loads visible rows by ID, which
// gives cheap random access for a virtualized list (no OFFSET scans).
//
// Each combination of filters has its own complete statement.
func (r *ChannelRepository) ListIDs(ctx context.Context, f model.ChannelFilter) ([]int64, error) {
	search := ftsQuery(f.Query)
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case f.Group == nil && !f.FavoritesOnly && search == "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels c
			WHERE c.playlist_id = ?
			ORDER BY c.position`,
			f.PlaylistID)

	case f.Group != nil && !f.FavoritesOnly && search == "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels c
			WHERE c.playlist_id = ?
			  AND c.group_title = ?
			ORDER BY c.position`,
			f.PlaylistID, *f.Group)

	case f.Group == nil && f.FavoritesOnly && search == "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels c
			WHERE c.playlist_id = ?
			  AND EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)
			ORDER BY c.position`,
			f.PlaylistID)

	case f.Group != nil && f.FavoritesOnly && search == "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels c
			WHERE c.playlist_id = ?
			  AND c.group_title = ?
			  AND EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)
			ORDER BY c.position`,
			f.PlaylistID, *f.Group)

	case f.Group == nil && !f.FavoritesOnly && search != "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels_fts
			JOIN channels c ON c.id = channels_fts.rowid
			WHERE c.playlist_id = ?
			  AND channels_fts MATCH ?
			ORDER BY c.position`,
			f.PlaylistID, search)

	case f.Group != nil && !f.FavoritesOnly && search != "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels_fts
			JOIN channels c ON c.id = channels_fts.rowid
			WHERE c.playlist_id = ?
			  AND c.group_title = ?
			  AND channels_fts MATCH ?
			ORDER BY c.position`,
			f.PlaylistID, *f.Group, search)

	case f.Group == nil && f.FavoritesOnly && search != "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels_fts
			JOIN channels c ON c.id = channels_fts.rowid
			WHERE c.playlist_id = ?
			  AND EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)
			  AND channels_fts MATCH ?
			ORDER BY c.position`,
			f.PlaylistID, search)

	case f.Group != nil && f.FavoritesOnly && search != "":
		rows, err = r.q.QueryContext(ctx, `
			SELECT c.id
			FROM channels_fts
			JOIN channels c ON c.id = channels_fts.rowid
			WHERE c.playlist_id = ?
			  AND c.group_title = ?
			  AND EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)
			  AND channels_fts MATCH ?
			ORDER BY c.position`,
			f.PlaylistID, *f.Group, search)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListByIDs returns the list fields of the channels with the given IDs, in
// the order given. Unknown IDs are skipped. The IDs are bound as one JSON
// array, so there is no limit on how many can be requested.
func (r *ChannelRepository) ListByIDs(ctx context.Context, ids []int64) ([]model.Channel, error) {
	if len(ids) == 0 {
		return []model.Channel{}, nil
	}
	idList, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.QueryContext(ctx, `
		SELECT
			c.id,
			c.name,
			c.group_title,
			EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)
		FROM channels c
		WHERE c.id IN (SELECT value FROM json_each(?))`, string(idList))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[int64]model.Channel, len(ids))
	for rows.Next() {
		var c model.Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Group, &c.Favorite); err != nil {
			return nil, err
		}
		byID[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]model.Channel, 0, len(ids))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// Get returns one channel with all its fields.
func (r *ChannelRepository) Get(ctx context.Context, id int64) (model.Channel, error) {
	c := model.Channel{ID: id}
	var opts string
	err := r.q.QueryRowContext(ctx, `
		SELECT
			c.name,
			c.url,
			c.group_title,
			c.tvg_id,
			c.vlc_opts,
			EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)
		FROM channels c
		WHERE c.id = ?`, id).Scan(&c.Name, &c.URL, &c.Group, &c.TvgID, &opts, &c.Favorite)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Channel{}, fmt.Errorf("channel %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.Channel{}, err
	}
	if err := json.Unmarshal([]byte(opts), &c.Options); err != nil {
		return model.Channel{}, fmt.Errorf("channel %d options: %w", id, err)
	}
	return c, nil
}

// FavoriteKey returns the playlist and favorite key of a channel, which is
// what FavoriteRepository stores.
func (r *ChannelRepository) FavoriteKey(ctx context.Context, id int64) (playlistID int64, key string, err error) {
	err = r.q.QueryRowContext(ctx, `
		SELECT playlist_id, fav_key
		FROM channels
		WHERE id = ?`, id).Scan(&playlistID, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("channel %d: %w", id, ErrNotFound)
	}
	return playlistID, key, err
}

// DeleteAllInPlaylist removes a playlist's channels and their search index
// entries. Run it inside a transaction.
//
// The index is cleared in one statement (a per-row delete trigger costs as
// much as the insert trigger did), and before the rows are deleted, because
// an external-content FTS5 delete needs the indexed values.
func (r *ChannelRepository) DeleteAllInPlaylist(ctx context.Context, playlistID int64) error {
	if _, err := r.q.ExecContext(ctx, `
		INSERT INTO channels_fts (channels_fts, rowid, name, group_title)
		SELECT 'delete', id, name, group_title
		FROM channels
		WHERE playlist_id = ?`, playlistID); err != nil {
		return err
	}
	_, err := r.q.ExecContext(ctx, `
		DELETE FROM channels
		WHERE playlist_id = ?`, playlistID)
	return err
}

// IndexAllInPlaylist adds a playlist's channels to the search index. Call it
// once after inserting them all: one bulk insert is much faster than
// indexing row by row.
func (r *ChannelRepository) IndexAllInPlaylist(ctx context.Context, playlistID int64) error {
	_, err := r.q.ExecContext(ctx, `
		INSERT INTO channels_fts (rowid, name, group_title)
		SELECT id, name, group_title
		FROM channels
		WHERE playlist_id = ?`, playlistID)
	return err
}

// insertBatch is the number of channels written per INSERT.
const insertBatch = 1000

// ChannelInserter appends channels to one playlist, in the order given.
// Rows are buffered and written insertBatch at a time; call Flush after the
// last Insert and Close when done.
type ChannelInserter struct {
	stmt       *sql.Stmt
	playlistID int64
	buf        []channelRow
	count      int
}

// channelRow is one element of the JSON array bound to the insert statement.
type channelRow struct {
	Position int    `json:"position"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Group    string `json:"group_title"`
	TvgID    string `json:"tvg_id"`
	TvgName  string `json:"tvg_name"`
	TvgLogo  string `json:"tvg_logo"`
	VLCOpts  string `json:"vlc_opts"`
	FavKey   string `json:"fav_key"`
}

// NewInserter prepares a ChannelInserter for a playlist. New rows are not
// searchable until IndexAllInPlaylist runs.
//
// A batch is bound as a single JSON array and expanded with json_each, so
// one fixed statement inserts any number of rows; a multi-row VALUES list
// would need SQL built to the batch size.
func (r *ChannelRepository) NewInserter(ctx context.Context, playlistID int64) (*ChannelInserter, error) {
	stmt, err := r.q.PrepareContext(ctx, `
		INSERT INTO channels
			(playlist_id, position, name, url, group_title, tvg_id, tvg_name, tvg_logo, vlc_opts, fav_key)
		SELECT
			?,
			value ->> '$.position',
			value ->> '$.name',
			value ->> '$.url',
			value ->> '$.group_title',
			value ->> '$.tvg_id',
			value ->> '$.tvg_name',
			value ->> '$.tvg_logo',
			value ->> '$.vlc_opts',
			value ->> '$.fav_key'
		FROM json_each(?)`)
	if err != nil {
		return nil, err
	}
	return &ChannelInserter{stmt: stmt, playlistID: playlistID, buf: make([]channelRow, 0, insertBatch)}, nil
}

// Insert buffers one channel, writing the buffer when it is full.
func (i *ChannelInserter) Insert(ctx context.Context, c model.ChannelInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	opts := "{}"
	if len(c.Options) > 0 {
		b, err := json.Marshal(c.Options)
		if err != nil {
			return err
		}
		opts = string(b)
	}
	i.buf = append(i.buf, channelRow{
		Position: i.count,
		Name:     c.Name,
		URL:      c.URL,
		Group:    c.Group,
		TvgID:    c.TvgID,
		TvgName:  c.TvgName,
		TvgLogo:  c.TvgLogo,
		VLCOpts:  opts,
		FavKey:   favKey(c.TvgID, c.Name),
	})
	i.count++
	if len(i.buf) == insertBatch {
		return i.Flush(ctx)
	}
	return nil
}

// Flush writes any buffered channels.
func (i *ChannelInserter) Flush(ctx context.Context) error {
	if len(i.buf) == 0 {
		return nil
	}
	rows, err := json.Marshal(i.buf)
	if err != nil {
		return err
	}
	i.buf = i.buf[:0]
	_, err = i.stmt.ExecContext(ctx, i.playlistID, string(rows))
	return err
}

// Count is the number of channels inserted so far, including buffered ones.
func (i *ChannelInserter) Count() int { return i.count }

// Close releases the prepared statement. Buffered rows are discarded.
func (i *ChannelInserter) Close() error { return i.stmt.Close() }

// favKey identifies a channel for favorites. It deliberately excludes the
// URL (which often embeds account credentials that change) and the group
// (which providers reshuffle). tvg-id alone is not enough because HD/SD
// variants usually share one, so the name is included.
func favKey(tvgID, name string) string {
	return strings.ToLower(strings.TrimSpace(tvgID)) + "\x1f" + strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// ftsQuery turns user input into a safe FTS5 prefix query: every word is
// quoted (so FTS syntax in the input is treated literally) and prefix-matched.
// The result is a bound MATCH argument, never part of the SQL text.
func ftsQuery(input string) string {
	var terms []string
	for _, w := range strings.Fields(input) {
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}
