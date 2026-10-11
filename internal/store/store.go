// Package store persists playlists, channels, favorites and settings in a
// local SQLite database.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
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

// PathEnv overrides the database location (development and testing).
const PathEnv = "IPTV_CLIENT_DB"

// ResolvePath returns the database location: $IPTV_CLIENT_DB if set,
// otherwise DefaultPath.
func ResolvePath() (string, error) {
	if p := os.Getenv(PathEnv); p != "" {
		return filepath.Abs(p)
	}
	return DefaultPath()
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

// migrationFiles holds the schema migrations in golang-migrate's
// NNNNNN_name.{up,down}.sql layout. Never edit one that has shipped.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

func (s *Store) migrate(ctx context.Context) error {
	if err := s.upgradeLegacyVersionTable(ctx); err != nil {
		return err
	}
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	drv, err := sqlite.WithInstance(s.db, &sqlite.Config{})
	if err != nil {
		return err
	}
	// Not m.Close(): that would close s.db, which the Store still owns.
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// upgradeLegacyVersionTable converts the schema_migrations table written by
// the pre-golang-migrate migrator (version only, one row per migration) to
// golang-migrate's format (a single version + dirty row), keeping the
// applied version so existing databases aren't migrated twice.
func (s *Store) upgradeLegacyVersionTable(ctx context.Context) error {
	var legacy bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')
   AND NOT EXISTS (SELECT 1 FROM pragma_table_info('schema_migrations') WHERE name = 'dirty')`).Scan(&legacy)
	if err != nil || !legacy {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	// Same table definition golang-migrate's sqlite driver creates.
	if _, err := tx.ExecContext(ctx, `
DROP TABLE schema_migrations;
CREATE TABLE schema_migrations (version uint64, dirty bool);
CREATE UNIQUE INDEX version_unique ON schema_migrations (version);`); err != nil {
		return err
	}
	if version > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, dirty) VALUES (?, false)`, version); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// favKey identifies a channel for favorites. It deliberately excludes the
// URL (which often embeds account credentials that change) and the group
// (which providers reshuffle). tvg-id alone is not enough because HD/SD
// variants usually share one, so the name is included.
func favKey(tvgID, name string) string {
	return strings.ToLower(strings.TrimSpace(tvgID)) + "\x1f" + strings.ToLower(strings.Join(strings.Fields(name), " "))
}
