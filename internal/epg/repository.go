package epg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"time"

	"iptv-client/internal/database"
)

// Window is how far ahead programmes are kept on import. Ended programmes
// are dropped, so a guide must be refreshed about daily to stay useful.
const Window = 48 * time.Hour

// insertBatch is the number of rows written per INSERT.
const insertBatch = 1000

// progressEvery is how many programmes are stored between progress callbacks.
const progressEvery = 5000

// ErrNoMatches is returned when a guide has no current programmes for any
// channel of the playlist. The previous guide data is kept.
var ErrNoMatches = errors.New("the program guide has no current programmes for this playlist's channels")

// Keys are the normalized tvg-ids and names of a playlist's channels, used
// to keep only the matching part of a guide.
type Keys struct {
	IDs   map[string]bool
	Names map[string]bool
}

// NewKeys normalizes the tvg-ids and names (tvg-name or title) of a
// playlist's channels with Key.
func NewKeys(ids, names []string) Keys {
	k := Keys{IDs: make(map[string]bool, len(ids)), Names: make(map[string]bool, len(names))}
	for _, id := range ids {
		if id = Key(id); id != "" {
			k.IDs[id] = true
		}
	}
	for _, n := range names {
		if n = Key(n); n != "" {
			k.Names[n] = true
		}
	}
	return k
}

// ImportResult describes an import.
type ImportResult struct {
	Channels   int // guide channels matched to the playlist
	Programmes int // programmes stored
}

// Repository reads and writes the guide_channels and guide_programmes tables.
type Repository struct {
	q database.Querier
}

// NewRepository returns a Repository that runs its statements on q.
func NewRepository(q database.Querier) *Repository { return &Repository{q: q} }

// DeleteAllInPlaylist removes a playlist's guide data.
func (r *Repository) DeleteAllInPlaylist(ctx context.Context, playlistID int64) error {
	if _, err := r.q.ExecContext(ctx, `
		DELETE FROM guide_programmes
		WHERE playlist_id = ?`, playlistID); err != nil {
		return err
	}
	_, err := r.q.ExecContext(ctx, `
		DELETE FROM guide_channels
		WHERE playlist_id = ?`, playlistID)
	return err
}

// Import replaces a playlist's guide data with the guide read from src,
// keeping the channels that match keys and their programmes that have not
// ended by now and start within Window. Run it inside a transaction: if it
// fails, rolling back keeps the old data. progress, if non-nil, is called
// with the running programme count.
func (r *Repository) Import(ctx context.Context, playlistID int64, src io.Reader, keys Keys, now time.Time,
	progress func(int)) (ImportResult, error) {

	if err := r.DeleteAllInPlaylist(ctx, playlistID); err != nil {
		return ImportResult{}, err
	}
	chans, err := newBatch(ctx, r.q, playlistID, `
		INSERT INTO guide_channels (playlist_id, guide_id, name_key, icon)
		SELECT ?, value ->> '$.id', value ->> '$.name', value ->> '$.icon'
		FROM json_each(?)`)
	if err != nil {
		return ImportResult{}, err
	}
	defer chans.Close()
	progs, err := newBatch(ctx, r.q, playlistID, `
		INSERT INTO guide_programmes (playlist_id, guide_id, start, stop, title, description)
		SELECT ?, value ->> '$.id', value ->> '$.start', value ->> '$.stop', value ->> '$.title', value ->> '$.desc'
		FROM json_each(?)`)
	if err != nil {
		return ImportResult{}, err
	}
	defer progs.Close()

	from, until := now.Unix(), now.Add(Window).Unix()
	matched := map[string]bool{} // guide IDs (as Key) with a guide_channels row
	var res ImportResult
	err = Parse(src, Handler{
		Channel: func(c Channel) error {
			id := Key(c.ID)
			if matched[id] {
				return nil // duplicate declaration
			}
			var names []string
			nameMatch := false
			for _, n := range c.Names {
				n = Key(n)
				names = append(names, n)
				nameMatch = nameMatch || keys.Names[n]
			}
			if !keys.IDs[id] && !nameMatch {
				return nil
			}
			matched[id] = true
			res.Channels++
			if len(names) == 0 {
				names = []string{""}
			}
			for _, n := range names {
				if err := chans.Add(ctx, channelRow{ID: id, Name: n, Icon: c.Icon}); err != nil {
					return err
				}
			}
			return nil
		},
		Programme: func(p Programme) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			start, stop := p.Start.Unix(), p.Stop.Unix()
			if stop <= from || start >= until {
				return nil
			}
			id := Key(p.Channel)
			if !matched[id] {
				// Guides should declare channels first, but a programme can
				// still match a tvg-id directly.
				if !keys.IDs[id] {
					return nil
				}
				matched[id] = true
				res.Channels++
				if err := chans.Add(ctx, channelRow{ID: id}); err != nil {
					return err
				}
			}
			res.Programmes++
			if progress != nil && res.Programmes%progressEvery == 0 {
				progress(res.Programmes)
			}
			return progs.Add(ctx, programmeRow{ID: id, Start: start, Stop: stop, Title: p.Title, Desc: p.Description})
		},
		Want: func(channel string) bool {
			id := Key(channel)
			return matched[id] || keys.IDs[id]
		},
	})
	if err != nil {
		return ImportResult{}, err
	}
	if res.Programmes == 0 {
		return ImportResult{}, ErrNoMatches
	}
	if err := chans.Flush(ctx); err != nil {
		return ImportResult{}, err
	}
	return res, progs.Flush(ctx)
}

type channelRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

type programmeRow struct {
	ID    string `json:"id"`
	Start int64  `json:"start"`
	Stop  int64  `json:"stop"`
	Title string `json:"title"`
	Desc  string `json:"desc"`
}

// batch buffers rows for one playlist and inserts them insertBatch at a
// time, bound as one JSON array (see channel.Repository.newInserter).
type batch struct {
	stmt       *sql.Stmt
	playlistID int64
	buf        []any
}

func newBatch(ctx context.Context, q database.Querier, playlistID int64, query string) (*batch, error) {
	stmt, err := q.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &batch{stmt: stmt, playlistID: playlistID, buf: make([]any, 0, insertBatch)}, nil
}

func (b *batch) Add(ctx context.Context, row any) error {
	b.buf = append(b.buf, row)
	if len(b.buf) == insertBatch {
		return b.Flush(ctx)
	}
	return nil
}

func (b *batch) Flush(ctx context.Context) error {
	if len(b.buf) == 0 {
		return nil
	}
	rows, err := json.Marshal(b.buf)
	if err != nil {
		return err
	}
	b.buf = b.buf[:0]
	_, err = b.stmt.ExecContext(ctx, b.playlistID, string(rows))
	return err
}

func (b *batch) Close() error { return b.stmt.Close() }
