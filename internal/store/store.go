// Package store persists playlists, channels, favorites and settings in a
// local SQLite database.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// AppDirName is the folder created under the OS config directory
// (%AppData% on Windows, ~/Library/Application Support on macOS).
const AppDirName = "IPTVClient"

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// DefaultPath returns the default database location for this OS user.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppDirName, "iptv.db"), nil
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrations are applied in order; never edit one that has shipped.
var migrations = []string{
	// 1: initial schema
	`
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
`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	for i := current; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// favKey identifies a channel for favorites. It deliberately excludes the
// URL (which often embeds account credentials that change) and the group
// (which providers reshuffle). tvg-id alone is not enough because HD/SD
// variants usually share one, so the name is included.
func favKey(tvgID, name string) string {
	return strings.ToLower(strings.TrimSpace(tvgID)) + "\x1f" + strings.ToLower(strings.Join(strings.Fields(name), " "))
}
