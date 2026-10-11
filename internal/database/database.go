// Package database owns the local SQLite database: opening it, applying the
// schema migrations and running transactions. The repositories that read
// and write its tables live in the domain packages (playlist, channel,
// setting); each runs its statements on a Querier, which is either the DB
// itself or a transaction started with DB.InTx.
//
// Every SQL statement is written out in full as a string literal. Queries
// are never assembled by concatenation, even when that means repeating
// near-identical statements.
package database

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

// Querier is what the repositories need to run statements. Both *DB and
// *sql.Tx implement it.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// DB is the SQLite connection. Each statement run on it directly is its own
// transaction; use InTx to group statements atomically.
type DB struct {
	*sql.DB
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
func Open(ctx context.Context, path string) (*DB, error) {
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
	d := &DB{DB: conn}
	if err := d.migrate(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	return d, nil
}

// InTx runs fn with one transaction; build repositories on tx to use it.
// The transaction is committed if fn returns nil and rolled back otherwise.
func (d *DB) InTx(ctx context.Context, fn func(tx Querier) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// migrationFiles holds the schema migrations in golang-migrate's
// NNNNNN_name.{up,down}.sql layout. Never edit one that has shipped.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

func (d *DB) migrate(ctx context.Context) error {
	if err := d.upgradeLegacyVersionTable(ctx); err != nil {
		return err
	}
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	drv, err := sqlite.WithInstance(d.DB, &sqlite.Config{})
	if err != nil {
		return err
	}
	// Not m.Close(): that would close the connection, which d still owns.
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
func (d *DB) upgradeLegacyVersionTable(ctx context.Context) error {
	var legacy bool
	err := d.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')
   AND NOT EXISTS (SELECT 1 FROM pragma_table_info('schema_migrations') WHERE name = 'dirty')`).Scan(&legacy)
	if err != nil || !legacy {
		return err
	}
	tx, err := d.BeginTx(ctx, nil)
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

// ExpectOne turns "no rows affected" into ErrNotFound. Pass it the results
// of an ExecContext call.
func ExpectOne(res sql.Result, err error) error {
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
