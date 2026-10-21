package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func openTest(t testing.TB) *DB {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "sub", "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	for range 2 {
		d, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
}

func TestMigrateUpgradesLegacyVersionTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// The legacy migrator only knew migrations 1 and 2.
	if err := newMigrate(t, d).Migrate(2); err != nil {
		t.Fatal(err)
	}
	// Rewrite the version table as the pre-golang-migrate migrator left it.
	if _, err := d.ExecContext(ctx, `
DROP TABLE schema_migrations;
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
INSERT INTO schema_migrations(version) VALUES (1), (2);`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d, err = Open(ctx, path) // would fail re-creating tables if versions were lost
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var version int
	var dirty bool
	if err := d.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version < 2 || dirty {
		t.Fatalf("got version %d dirty %v, want at least 2 and false", version, dirty)
	}
}

func newMigrate(t *testing.T, d *DB) *migrate.Migrate {
	t.Helper()
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	drv, err := sqlite.WithInstance(d.DB, &sqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMigrationsDownAndUp(t *testing.T) {
	m := newMigrate(t, openTest(t))
	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up after down: %v", err)
	}
}

func TestPlaylistPositionMigrationKeepsNameOrder(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	m := newMigrate(t, d)
	if err := m.Migrate(3); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `
INSERT INTO playlists (id, name, source_type, source, created_at) VALUES
	(1, 'b', 'url', 'x', 0), (2, 'C', 'url', 'x', 0), (3, 'a', 'url', 'x', 0), (4, 'a', 'url', 'x', 0);`); err != nil {
		t.Fatal(err)
	}
	if err := m.Up(); err != nil {
		t.Fatal(err)
	}
	rows, err := d.QueryContext(ctx, `SELECT id FROM playlists ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if fmt.Sprint(got) != "[3 4 1 2]" {
		t.Errorf("order after migration = %v, want [3 4 1 2]", got)
	}
}

func TestInTxRollsBack(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	boom := errors.New("boom")
	err := d.InTx(ctx, func(tx Querier) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('k', 'v')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rolled-back row kept: count %d, %v", n, err)
	}
}
