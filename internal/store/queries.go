package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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
}

// Channel is one playable entry.
type Channel struct {
	ID         int64
	PlaylistID int64
	Position   int
	Name       string
	URL        string
	Group      string
	TvgID      string
	TvgName    string
	TvgLogo    string
	Options    map[string]string
	Favorite   bool
}

// ChannelInput is a channel to insert during a refresh.
type ChannelInput struct {
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

// ChannelFilter selects channels. Zero values mean "no filter".
type ChannelFilter struct {
	PlaylistID    int64
	Group         *string // nil = all groups; "" = ungrouped
	FavoritesOnly bool
	Query         string // full-text search
}

// --- playlists ---

// AddPlaylist inserts a playlist (with no channels yet) and returns its ID.
func (s *Store) AddPlaylist(ctx context.Context, name string, t SourceType, source string) (int64, error) {
	if t != SourceURL && t != SourceFile {
		return 0, fmt.Errorf("invalid source type %q", t)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO playlists(name, source_type, source, created_at) VALUES (?, ?, ?, ?)`,
		name, string(t), source, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const playlistCols = `id, name, source_type, source, etag, last_modified, last_refreshed_at, channel_count, created_at`

func scanPlaylist(row interface{ Scan(...any) error }) (Playlist, error) {
	var p Playlist
	var refreshed, created int64
	err := row.Scan(&p.ID, &p.Name, &p.SourceType, &p.Source, &p.ETag, &p.LastModified, &refreshed, &p.ChannelCount, &created)
	if refreshed > 0 {
		p.LastRefreshedAt = time.Unix(refreshed, 0)
	}
	p.CreatedAt = time.Unix(created, 0)
	return p, err
}

// GetPlaylist returns one playlist.
func (s *Store) GetPlaylist(ctx context.Context, id int64) (Playlist, error) {
	p, err := scanPlaylist(s.db.QueryRowContext(ctx, `SELECT `+playlistCols+` FROM playlists WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("playlist %d: %w", id, ErrNotFound)
	}
	return p, err
}

// ListPlaylists returns all playlists ordered by name.
func (s *Store) ListPlaylists(ctx context.Context) ([]Playlist, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+playlistCols+` FROM playlists ORDER BY name COLLATE NOCASE, id`)
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

// RenamePlaylist changes a playlist's display name.
func (s *Store) RenamePlaylist(ctx context.Context, id int64, name string) error {
	return expectOne(s.db.ExecContext(ctx, `UPDATE playlists SET name = ? WHERE id = ?`, name, id))
}

// DeletePlaylist removes a playlist with its channels and favorites.
func (s *Store) DeletePlaylist(ctx context.Context, id int64) error {
	return expectOne(s.db.ExecContext(ctx, `DELETE FROM playlists WHERE id = ?`, id))
}

// MarkNotModified records a successful refresh that returned no new data.
func (s *Store) MarkNotModified(ctx context.Context, id int64) error {
	return expectOne(s.db.ExecContext(ctx, `UPDATE playlists SET last_refreshed_at = ? WHERE id = ?`, time.Now().Unix(), id))
}

// ReplaceChannels atomically replaces all channels of a playlist. produce is
// called with an insert function and should feed it every channel; if
// produce returns an error the transaction is rolled back and the previous
// channels remain. etag/lastModified are stored with the new data.
func (s *Store) ReplaceChannels(ctx context.Context, playlistID int64, etag, lastModified string,
	produce func(insert func(ChannelInput) error) error) (int, error) {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() // no-op after Commit

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM playlists WHERE id = ?`, playlistID).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, fmt.Errorf("playlist %d: %w", playlistID, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE playlist_id = ?`, playlistID); err != nil {
		return 0, err
	}
	// Rows are buffered and written insertBatch at a time with one multi-row
	// INSERT, which is much faster than a statement per row.
	const cols = 10
	insertSQL := func(rows int) string {
		row := "(?" + strings.Repeat(",?", cols-1) + ")"
		return `INSERT INTO channels
			(playlist_id, position, name, url, group_title, tvg_id, tvg_name, tvg_logo, vlc_opts, fav_key)
			VALUES ` + row + strings.Repeat(","+row, rows-1)
	}
	full, err := tx.PrepareContext(ctx, insertSQL(insertBatch))
	if err != nil {
		return 0, err
	}
	defer full.Close()

	n := 0
	buf := make([]any, 0, insertBatch*cols)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		var err error
		if len(buf) == cap(buf) {
			_, err = full.ExecContext(ctx, buf...)
		} else {
			_, err = tx.ExecContext(ctx, insertSQL(len(buf)/cols), buf...)
		}
		buf = buf[:0]
		return err
	}
	insert := func(c ChannelInput) error {
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
		buf = append(buf, playlistID, n, c.Name, c.URL, c.Group, c.TvgID, c.TvgName, c.TvgLogo, opts, favKey(c.TvgID, c.Name))
		n++
		if len(buf) == cap(buf) {
			return flush()
		}
		return nil
	}
	if err := produce(insert); err != nil {
		return 0, err
	}
	if err := flush(); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO channels_fts(rowid, name, group_title)
		SELECT id, name, group_title FROM channels WHERE playlist_id = ?`, playlistID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE playlists SET channel_count = ?, etag = ?, last_modified = ?, last_refreshed_at = ? WHERE id = ?`,
		n, etag, lastModified, time.Now().Unix(), playlistID); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// --- channels ---

// ListGroups returns the groups of a playlist in playlist order, with counts.
func (s *Store) ListGroups(ctx context.Context, playlistID int64) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT group_title, COUNT(*) FROM channels WHERE playlist_id = ?
		GROUP BY group_title ORDER BY MIN(position)`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Name, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ChannelIDs returns the IDs of all channels matching f, in display order.
// The UI fetches this list once and then loads visible rows by ID, which
// gives cheap random access for a virtualized list (no OFFSET scans).
func (s *Store) ChannelIDs(ctx context.Context, f ChannelFilter) ([]int64, error) {
	var (
		where = []string{"c.playlist_id = ?"}
		args  = []any{f.PlaylistID}
		from  = "channels c"
	)
	if f.Group != nil {
		where = append(where, "c.group_title = ?")
		args = append(args, *f.Group)
	}
	if f.FavoritesOnly {
		where = append(where, "EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)")
	}
	if q := ftsQuery(f.Query); q != "" {
		from = "channels_fts JOIN channels c ON c.id = channels_fts.rowid"
		where = append(where, "channels_fts MATCH ?")
		args = append(args, q)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id FROM `+from+` WHERE `+strings.Join(where, " AND ")+` ORDER BY c.position`, args...)
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

// insertBatch is the number of rows per multi-row INSERT (10 columns each,
// well under SQLite's 32766 bound-variable limit).
const insertBatch = 200

// maxBatch bounds ChannelsByIDs to stay under SQLite's variable limit.
const maxBatch = 500

// ChannelsByIDs returns the channels with the given IDs, in the order given.
// Unknown IDs are skipped.
func (s *Store) ChannelsByIDs(ctx context.Context, ids []int64) ([]Channel, error) {
	if len(ids) == 0 {
		return []Channel{}, nil
	}
	if len(ids) > maxBatch {
		return nil, fmt.Errorf("at most %d ids per request", maxBatch)
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.playlist_id, c.position, c.name, c.url, c.group_title, c.tvg_id, c.tvg_name, c.tvg_logo, c.vlc_opts,
		       EXISTS (SELECT 1 FROM favorites fv WHERE fv.playlist_id = c.playlist_id AND fv.fav_key = c.fav_key)
		FROM channels c WHERE c.id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[int64]Channel, len(ids))
	for rows.Next() {
		var c Channel
		var opts string
		if err := rows.Scan(&c.ID, &c.PlaylistID, &c.Position, &c.Name, &c.URL, &c.Group, &c.TvgID, &c.TvgName, &c.TvgLogo, &opts, &c.Favorite); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(opts), &c.Options); err != nil {
			return nil, fmt.Errorf("channel %d options: %w", c.ID, err)
		}
		byID[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(ids))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// GetChannel returns one channel.
func (s *Store) GetChannel(ctx context.Context, id int64) (Channel, error) {
	cs, err := s.ChannelsByIDs(ctx, []int64{id})
	if err != nil {
		return Channel{}, err
	}
	if len(cs) == 0 {
		return Channel{}, fmt.Errorf("channel %d: %w", id, ErrNotFound)
	}
	return cs[0], nil
}

// ftsQuery turns user input into a safe FTS5 prefix query: every word is
// quoted (so FTS syntax in the input is treated literally) and prefix-matched.
func ftsQuery(input string) string {
	var terms []string
	for _, w := range strings.Fields(input) {
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}

// --- favorites ---

// SetFavorite marks or unmarks a channel as favorite.
func (s *Store) SetFavorite(ctx context.Context, channelID int64, fav bool) error {
	var pid int64
	var key string
	err := s.db.QueryRowContext(ctx, `SELECT playlist_id, fav_key FROM channels WHERE id = ?`, channelID).Scan(&pid, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("channel %d: %w", channelID, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if fav {
		_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO favorites(playlist_id, fav_key, created_at) VALUES (?, ?, ?)`, pid, key, time.Now().Unix())
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM favorites WHERE playlist_id = ? AND fav_key = ?`, pid, key)
	}
	return err
}

// --- settings ---

// GetSetting returns a setting value, or "" if unset.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a setting; an empty value deletes it.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	var err error
	if value == "" {
		_, err = s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	}
	return err
}

func expectOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
