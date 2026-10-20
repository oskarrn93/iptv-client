-- Program guide (XMLTV/EPG). guide_url is set by the user and overrides
-- tvg_url, which comes from the playlist's #EXTM3U header.
ALTER TABLE playlists ADD COLUMN guide_url TEXT NOT NULL DEFAULT '';
ALTER TABLE playlists ADD COLUMN tvg_url TEXT NOT NULL DEFAULT '';
ALTER TABLE playlists ADD COLUMN guide_refreshed_at INTEGER NOT NULL DEFAULT 0;

-- Only the guide channels that match a channel of the playlist are stored,
-- one row per display name. Keys are normalized by epg.Key.
CREATE TABLE guide_channels (
	playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
	guide_id    TEXT    NOT NULL,
	name_key    TEXT    NOT NULL,
	icon        TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX guide_channels_by_id   ON guide_channels(playlist_id, guide_id);
CREATE INDEX guide_channels_by_name ON guide_channels(playlist_id, name_key);

CREATE TABLE guide_programmes (
	playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
	guide_id    TEXT    NOT NULL,
	start       INTEGER NOT NULL,
	stop        INTEGER NOT NULL,
	title       TEXT    NOT NULL,
	description TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX guide_programmes_by_channel ON guide_programmes(playlist_id, guide_id, stop);
