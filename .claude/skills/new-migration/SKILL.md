---
name: new-migration
description: Scaffold the next numbered SQLite migration pair in internal/database/migrations and verify it applies.
argument-hint: <snake_case_description>
disable-model-invocation: true
---

# New database migration

Create a migration named `$ARGUMENTS`.

1. List `internal/database/migrations/` and take the highest `NNNNNN_` prefix
   plus one, zero-padded to six digits.
2. Create both files:
   - `NNNNNN_$ARGUMENTS.up.sql`
   - `NNNNNN_$ARGUMENTS.down.sql` that exactly reverses the up migration.
   Start each with a short `--` comment saying why, like the existing ones.
3. Write SQLite that `modernc.org/sqlite` accepts. Remember:
   - `channels` has an FTS index, `channels_fts`, maintained in bulk from Go
     (`DeleteAllInPlaylist` / `IndexAllInPlaylist` in
     `internal/channel/repository.go`), so schema changes to `channels` may
     need matching changes there.
   - Migrations are embedded and run on every `database.Open`; they must be
     safe on a database with 200k+ channels (avoid per-row triggers).
4. Update the affected package's `Repository` (SQL) and `Service`, e.g.
   `internal/channel`, `internal/playlist` or `internal/setting`.
5. Verify:
   ```
   go test ./internal/...
   ```
   If the down migration isn't covered by an existing test, add one to
   `internal/database/database_test.go` that migrates up, down one step and up
   again.
