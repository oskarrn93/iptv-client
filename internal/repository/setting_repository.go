package repository

import (
	"context"
	"database/sql"
	"errors"
)

// SettingRepository reads and writes the settings key/value table.
type SettingRepository struct {
	q Querier
}

// Get returns a setting value, or "" if unset.
func (r *SettingRepository) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.q.QueryRowContext(ctx, `
		SELECT value
		FROM settings
		WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Set stores a setting, replacing any previous value.
func (r *SettingRepository) Set(ctx context.Context, key, value string) error {
	_, err := r.q.ExecContext(ctx, `
		INSERT INTO settings (key, value)
		VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Delete removes a setting. Deleting a missing setting is a no-op.
func (r *SettingRepository) Delete(ctx context.Context, key string) error {
	_, err := r.q.ExecContext(ctx, `
		DELETE FROM settings
		WHERE key = ?`, key)
	return err
}
