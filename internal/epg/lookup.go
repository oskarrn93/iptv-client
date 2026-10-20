package epg

import (
	"context"
	"encoding/json"
	"time"
)

// Ref identifies a playlist channel for a guide lookup.
type Ref struct {
	ChannelID  int64
	PlaylistID int64
	TvgID      string
	TvgName    string
	Name       string
}

// Info is what the guide knows about a channel.
type Info struct {
	Icon       string      // guide icon URL, "" if none
	Programmes []Programme // starting with the one on now, if any
}

// Current returns, for each ref the guide matches, its icon and the
// programme on at the given time (if any). Channels the guide does not
// match are left out.
func (r *Repository) Current(ctx context.Context, refs []Ref, at time.Time) (map[int64]Info, error) {
	out := map[int64]Info{}
	for playlistID, refs := range byPlaylist(refs) {
		guideIDs, icons, err := r.resolve(ctx, playlistID, refs)
		if err != nil {
			return nil, err
		}
		if len(guideIDs) == 0 {
			continue
		}
		on, err := r.onNow(ctx, playlistID, guideIDs, at)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			id, ok := guideIDs[ref.ChannelID]
			if !ok {
				continue
			}
			info := Info{Icon: icons[id]}
			if p, ok := on[id]; ok {
				info.Programmes = []Programme{p}
			}
			out[ref.ChannelID] = info
		}
	}
	return out, nil
}

// Upcoming returns a channel's icon and up to n programmes that have not
// ended at the given time. ok is false if the guide does not match it.
func (r *Repository) Upcoming(ctx context.Context, ref Ref, at time.Time, n int) (_ Info, ok bool, err error) {
	guideIDs, icons, err := r.resolve(ctx, ref.PlaylistID, []Ref{ref})
	if err != nil {
		return Info{}, false, err
	}
	id, ok := guideIDs[ref.ChannelID]
	if !ok {
		return Info{}, false, nil
	}
	rows, err := r.q.QueryContext(ctx, `
		SELECT start, stop, title, description
		FROM guide_programmes
		WHERE playlist_id = ?
		  AND guide_id = ?
		  AND stop > ?
		ORDER BY start
		LIMIT ?`, ref.PlaylistID, id, at.Unix(), n)
	if err != nil {
		return Info{}, false, err
	}
	defer rows.Close()
	info := Info{Icon: icons[id], Programmes: []Programme{}}
	for rows.Next() {
		var start, stop int64
		p := Programme{Channel: id}
		if err := rows.Scan(&start, &stop, &p.Title, &p.Description); err != nil {
			return Info{}, false, err
		}
		p.Start, p.Stop = time.Unix(start, 0), time.Unix(stop, 0)
		info.Programmes = append(info.Programmes, p)
	}
	return info, true, rows.Err()
}

// resolve finds the guide channel of each ref in one playlist: by tvg-id,
// else by tvg-name, else by name. It returns the guide ID per channel ID
// and the icon per guide ID.
func (r *Repository) resolve(ctx context.Context, playlistID int64, refs []Ref) (map[int64]string, map[string]string, error) {
	var ids, names []string
	for _, ref := range refs {
		if k := Key(ref.TvgID); k != "" {
			ids = append(ids, k)
		}
		for _, n := range []string{ref.TvgName, ref.Name} {
			if k := Key(n); k != "" {
				names = append(names, k)
			}
		}
	}
	idList, err := json.Marshal(ids)
	if err != nil {
		return nil, nil, err
	}
	nameList, err := json.Marshal(names)
	if err != nil {
		return nil, nil, err
	}
	// Two SELECTs rather than one OR: without ANALYZE statistics SQLite
	// won't use both indexes for the OR and scans the whole playlist.
	rows, err := r.q.QueryContext(ctx, `
		SELECT rowid, guide_id, name_key, icon FROM guide_channels
		WHERE playlist_id = ? AND guide_id IN (SELECT value FROM json_each(?))
		UNION
		SELECT rowid, guide_id, name_key, icon FROM guide_channels
		WHERE playlist_id = ? AND name_key IN (SELECT value FROM json_each(?))
		ORDER BY 1`, playlistID, string(idList), playlistID, string(nameList))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	known := map[string]bool{}    // guide IDs
	byName := map[string]string{} // name key -> first guide ID with it
	icons := map[string]string{}
	for rows.Next() {
		var rowid int64
		var id, name, icon string
		if err := rows.Scan(&rowid, &id, &name, &icon); err != nil {
			return nil, nil, err
		}
		known[id] = true
		if _, ok := byName[name]; !ok && name != "" {
			byName[name] = id
		}
		if icons[id] == "" {
			icons[id] = icon
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	out := map[int64]string{}
	for _, ref := range refs {
		if k := Key(ref.TvgID); known[k] {
			out[ref.ChannelID] = k
		} else if id, ok := byName[Key(ref.TvgName)]; ok {
			out[ref.ChannelID] = id
		} else if id, ok := byName[Key(ref.Name)]; ok {
			out[ref.ChannelID] = id
		}
	}
	return out, icons, nil
}

// onNow returns the programme on at the given time per guide ID.
func (r *Repository) onNow(ctx context.Context, playlistID int64, guideIDs map[int64]string, at time.Time) (map[string]Programme, error) {
	ids := make([]string, 0, len(guideIDs))
	for _, id := range guideIDs {
		ids = append(ids, id)
	}
	idList, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.QueryContext(ctx, `
		SELECT guide_id, start, stop, title
		FROM guide_programmes
		WHERE playlist_id = ?
		  AND guide_id IN (SELECT value FROM json_each(?))
		  AND stop > ?
		  AND start <= ?
		ORDER BY start`, playlistID, string(idList), at.Unix(), at.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Programme{}
	for rows.Next() {
		var start, stop int64
		var p Programme
		if err := rows.Scan(&p.Channel, &start, &stop, &p.Title); err != nil {
			return nil, err
		}
		p.Start, p.Stop = time.Unix(start, 0), time.Unix(stop, 0)
		out[p.Channel] = p // overlapping entries: the later start wins
	}
	return out, rows.Err()
}

func byPlaylist(refs []Ref) map[int64][]Ref {
	out := map[int64][]Ref{}
	for _, ref := range refs {
		out[ref.PlaylistID] = append(out[ref.PlaylistID], ref)
	}
	return out
}
