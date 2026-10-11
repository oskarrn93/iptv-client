// Package repository persists playlists, channels, favorites and settings in
// a local SQLite database. There is one repository per table; each runs its
// statements either directly on the database or inside a transaction
// started with Database.InTx.
//
// Every SQL statement is written out in full as a string literal. Queries
// are never assembled by concatenation, even when that means repeating
// near-identical statements.
package repository

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

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

// Querier is what the repositories need to run statements. Both *sql.DB and
// *sql.Tx implement it.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// Repositories groups the repositories that share one Querier.
type Repositories struct {
	Playlists *PlaylistRepository
	Channels  *ChannelRepository
	Favorites *FavoriteRepository
	Settings  *SettingRepository
}

func newRepositories(q Querier) Repositories {
	return Repositories{
		Playlists: &PlaylistRepository{q: q},
		Channels:  &ChannelRepository{q: q},
		Favorites: &FavoriteRepository{q: q},
		Settings:  &SettingRepository{q: q},
	}
}

// Database owns the SQLite connection. Its embedded repositories run each
// statement on its own; use InTx to group statements atomically.
type Database struct {
	Repositories
	conn *sql.DB
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
func Open(ctx context.Context, path string) (*Database, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d := &Database{Repositories: newRepositories(conn), conn: conn}
	if err := d.migrate(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	return d, nil
}

// Close closes the database.
func (d *Database) Close() error { return d.conn.Close() }

// InTx runs fn with repositories bound to one transaction. The transaction
// is committed if fn returns nil and rolled back otherwise.
func (d *Database) InTx(ctx context.Context, fn func(tx Repositories) error) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	if err := fn(newRepositories(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// migrationFiles holds the schema migrations in golang-migrate's
// NNNNNN_name.{up,down}.sql layout. Never edit one that has shipped.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

func (d *Database) migrate(ctx context.Context) error {
	if err := d.upgradeLegacyVersionTable(ctx); err != nil {
		return err
	}
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	drv, err := sqlite.WithInstance(d.conn, &sqlite.Config{})
	if err != nil {
		return err
	}
	// Not m.Close(): that would close d.conn, which the Database still owns.
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
func (d *Database) upgradeLegacyVersionTable(ctx context.Context) error {
	var legacy bool
	err := d.conn.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')
   AND NOT EXISTS (SELECT 1 FROM pragma_table_info('schema_migrations') WHERE name = 'dirty')`).Scan(&legacy)
	if err != nil || !legacy {
		return err
	}
	tx, err := d.conn.BeginTx(ctx, nil)
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

// expectOne turns "no rows affected" into ErrNotFound.
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
