CREATE TABLE playlists (
	id                INTEGER PRIMARY KEY,
	name              TEXT    NOT NULL,
	source_type       TEXT    NOT NULL CHECK (source_type IN ('url', 'file')),
	source            TEXT    NOT NULL,
	etag              TEXT    NOT NULL DEFAULT '',
	last_modified     TEXT    NOT NULL DEFAULT '',
	last_refreshed_at INTEGER NOT NULL DEFAULT 0,
	channel_count     INTEGER NOT NULL DEFAULT 0,
	created_at        INTEGER NOT NULL
);

CREATE TABLE channels (
	id          INTEGER PRIMARY KEY,
	playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
	position    INTEGER NOT NULL,
	name        TEXT    NOT NULL,
	url         TEXT    NOT NULL,
	group_title TEXT    NOT NULL DEFAULT '',
	tvg_id      TEXT    NOT NULL DEFAULT '',
	tvg_name    TEXT    NOT NULL DEFAULT '',
	tvg_logo    TEXT    NOT NULL DEFAULT '',
	vlc_opts    TEXT    NOT NULL DEFAULT '{}',
	fav_key     TEXT    NOT NULL
);
CREATE INDEX channels_by_position ON channels(playlist_id, position);
CREATE INDEX channels_by_group    ON channels(playlist_id, group_title, position);
CREATE INDEX channels_by_fav_key  ON channels(playlist_id, fav_key);

-- Favorites reference channels by a key that survives refreshes and
-- credential changes in stream URLs (see favKey).
CREATE TABLE favorites (
	playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
	fav_key     TEXT    NOT NULL,
	created_at  INTEGER NOT NULL,
	PRIMARY KEY (playlist_id, fav_key)
);

CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE VIRTUAL TABLE channels_fts USING fts5(
	name, group_title,
	content = 'channels', content_rowid = 'id',
	tokenize = 'unicode61 remove_diacritics 2'
);
-- No insert trigger: per-row FTS inserts force an index flush per statement
-- and made 200k-channel imports ~2x slower. ReplaceChannels indexes new rows
-- in one INSERT ... SELECT instead. Deletes (incl. cascades) use a trigger.
CREATE TRIGGER channels_ad AFTER DELETE ON channels BEGIN
	INSERT INTO channels_fts(channels_fts, rowid, name, group_title) VALUES ('delete', old.id, old.name, old.group_title);
END;
