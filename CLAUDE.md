# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

IPTV Client: a Go + Wails v2 desktop app (Windows, macOS) that imports M3U/M3U8 playlists into SQLite, browses/searches them, and launches channels in an external player (VLC, mpv, IINA, MPC-HC, PotPlayer). See [README.md](README.md) for features and the security model; [docs/SETUP.md](docs/SETUP.md) is the full developer guide. Keep SETUP.md updated when adding a tool, install step or workflow.

## Commands

Run from the repo root. The Wails CLI is pinned in `go.mod` as a Go tool: always `go tool wails ...`, never a global `wails`.

| Task | Command |
|---|---|
| Go tests (core) | `go test ./internal/... ./cmd/...` |
| Single Go test | `go test ./internal/channel -run TestName` |
| Skip the 200k-row perf test | `go test -short ./internal/...` |
| Fuzz the parser (CI/Linux only, see below) | `go test ./internal/m3u -run '^$' -fuzz FuzzParse -fuzztime 60s` |
| Format / vet (CI enforces both) | `gofmt -l .` (must print nothing), `go vet ./internal/... ./cmd/...` |
| UI tests (builds `dist`, headless Edge) | `cd frontend && npm run test:e2e` (`E2E_BROWSER=chrome`, `E2E_SCREENSHOTS=./shots` optional) |
| Update README screenshots (fictional demo data, `docs/screenshot*.png`) | `cd frontend && npm run screenshots` |
| Production build | `go tool wails build -clean -trimpath` → `build/bin/` |
| Build without regenerating bindings | `go tool wails build -skipbindings` |
| Regenerate TS bindings after changing `App`'s exported API | `go tool wails generate module`, then commit `frontend/wailsjs` |
| Debug CLI (same services, no GUI) | `go run ./cmd/iptv-debug -db ./scratch.db -h` |
| App on a scratch DB | `IPTV_CLIENT_DB=./scratch.db build/bin/iptv-client.exe` |

`go test ./...` from the root needs `frontend/dist` to exist (`main.go` embeds it); test `./internal/... ./cmd/...` instead, or create a placeholder dir. Do not use `-upx`.

### Windows Smart App Control

The dev machine has Smart App Control on, which blocks some locally built unsigned binaries: a globally installed `wails.exe`, the fuzz test binary, sometimes Wails' `wailsbindings.exe` and the test binary of a new or changed package, and the `wails dev` binary (so hot reload doesn't work). Workarounds: `go tool wails`, `-skipbindings` when the Go API is unchanged, fuzzing in CI, running blocked package tests in WSL (`GOOS=linux go test -c -o /tmp/x.test ./internal/x && wsl.exe --cd "$(cygpath -w /tmp)" ./x.test`), and checking UI changes with `npm run test:e2e` rather than `wails dev`. In PowerShell, `npm` may need the machine PATH reloaded first.

## Architecture

**Go side.** `main.go` embeds `frontend/dist` and binds a single `App` (`app.go`) to the frontend. Every exported `App` method is callable from JS and generates `frontend/wailsjs` bindings and TS models (the DTOs in `app.go`). CI fails if the committed bindings are stale. Domain logic lives in `internal/`, one package per area, each with a `Service` (business rules) over a `Repository` (SQL):

- `database`: SQLite via pure-Go `modernc.org/sqlite` (no CGO), golang-migrate migrations embedded from `internal/database/migrations/` (numbered `.up.sql`/`.down.sql` pairs) and run on `Open`. Repositories take a `database.Querier`, so the same repo works on the DB or inside `DB.InTx`. Build repositories on the `tx` for atomic work.
- `playlist`: add/refresh/rename/delete. A refresh streams `fetch` → `m3u.Parse` → per-entry `player.ValidateURL`/`SanitizeOptions` → `channel.Repository.ReplaceAllInPlaylist` (batched insert, then one bulk search-index update), all inside one transaction (`replaceChannels`). A failed import rolls back and keeps the old channels. URL refreshes use ETag/Last-Modified and enforce a minimum interval (`TooSoonError`) unless forced.
- `channel`: groups, FTS search, favorites, play. Favorites are keyed on tvg-id + name (`favKey`), not URL, so they survive provider credential changes across refreshes.
- `epg`: XMLTV program guide: streaming parser (gzip, any IANA charset), `Import` (keeps only the playlist's channels, next 48 h) and now/next lookups that match a channel by tvg-id, then tvg-name, then name (`Key` normalizes). `playlist.Service.RefreshGuide` runs the import in one transaction; the guide URL (user's `guide_url`, else the header's `tvg_url`) lives on the playlist.
- `logo`: logo download + disk cache (`logos/` next to the DB). The UI loads `/logo/<channel id>`, served by `serveLogo` (`logos.go`) as the Wails AssetServer `Handler`; logo URLs never reach the UI.
- `m3u`: streaming parser (handles BOM/CRLF, Latin-1, plain URL lists, and detects bare HLS playlists). Fixtures are in `testdata/`.
- `fetch`: download with a size cap and conditional requests. Errors must never include the URL (it carries credentials).
- `player`: player discovery per OS (`find_*.go`), per-player argument profiles, the URL scheme and `#EXTVLCOPT` allowlists, and the direct exec launch (never through a shell).
- `update`: the in-app updater reads GitHub's latest release. Per-OS `Platform` strategies are in `windows.go` and `darwin.go`. The release asset names are a contract with `.github/workflows/build.yml`.
- `importguard.go`: only one import runs at a time, and the UI can cancel it. Progress reaches the UI through Wails events (`ImportProgressEvent`, `UpdateProgressEvent`).
- `logging`: `log/slog` text logs in daily, size-rotated, pruned files (`%LocalAppData%\IPTVClient\logs`, `~/Library/Logs/IPTVClient`, or next to `$IPTV_CLIENT_DB`). `Setup` returns `Logs`, used only in `main.go`; everything else takes a plain `*slog.Logger` (and `App` the `*slog.LevelVar`). Also the Wails logger adapter (Trace dropped), `UI` for frontend lines (`App.Log`, `component=ui`, validated and rate-limited) and crash capture (`debug.SetCrashOutput`, logged at the next start). `redact` is its backstop; `desktop.OpenFolder` opens the folder.

**Frontend.** `frontend/src` is Vite + TypeScript with no framework. `channel-list.ts` is a virtualized list: it fetches all matching channel IDs (`ChannelIDs`), then loads row data in chunks for the visible window (`Channels`). Use this ID-list paging pattern for large result sets. The e2e tests (`frontend/e2e/run.mjs`) serve the production build and replace the Go backend with an in-memory stub (`e2e/stub-backend.js`, 50k channels including HTML-laden names). **When you add or change an `App` method, update the stub too.**

## Security invariants (playlists are untrusted input)

- No HTML sinks in `frontend/src`: CI greps for and rejects `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write`, `eval(`, `new Function`. Build DOM with the `h()`/`append` helpers in `dom.ts`. The production CSP forbids string evaluation, which is why e2e uses its own `waitFor` and not Playwright's `waitForFunction`.
- Bound methods accept only IDs and plain values. File paths (player executable, local playlist) are chosen through native dialogs opened from Go, never passed in from JS.
- Stream URLs and options never reach the UI. The `Channel` DTO omits them, playlist sources are redacted, and copy-URL writes to the clipboard from Go.
- Any new player argument or option must go through the allowlists in `internal/player`.
- Never log URLs, stream options, the User-Agent, file paths or channel/playlist names; log IDs (`playlist_id`, `channel_id`). The handler redacts every value with `internal/redact` as a backstop, and `internal/logging/leak_test.go` must keep passing.

## Conventions

- Frontend TS uses modern syntax: `async`/`await` with `try`/`catch`, not `.then()` chains.
- Logging: use the `*Context` slog methods where a `ctx` exists. Bound methods start with `ctx, end := a.call("op", attrs...)` and `defer end(&err)`, which tags lines with `op` and logs a returned error (Debug if expected, else Warn). Frontend code logs through `log.ts`, not `console.*`.
- Releases: push a `vX.Y.Z` tag. CI stamps the version (`-X main.version`, and `wails.json` via `.github/stamp-version.js`). Leave `productVersion` at `0.0.0` in the repo. Local builds report `dev`, and `dev` builds refuse to self-update.
- Edit `build/windows/installer/project.nsi`, not `wails_tools.nsh` (Wails regenerates it).
