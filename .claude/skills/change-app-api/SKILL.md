---
name: change-app-api
description: Checklist for adding, removing or changing an exported method or DTO on the Wails-bound App (app.go). Use whenever an exported App method signature or a DTO struct in app.go changes, so the bindings, the e2e stub backend and the security invariants stay in sync.
---

# Changing the App API

Every exported method on `App` in `app.go` is callable from JavaScript, and
its DTOs become TS models in `frontend/wailsjs`. A change touches four places;
CI fails if any is missed.

## 1. Design the method against the invariants

- Parameters are IDs and plain values only. **Never accept a file path** from
  JS: open a native dialog from Go instead (see `AddPlaylistFile`, `ChoosePlayer`).
- Return values must not carry stream URLs, `#EXTVLCOPT` options or unredacted
  playlist sources. If the UI needs to act on a URL, do it in Go (see
  `CopyStreamURL`).
- Start with `svc, err := a.ready()` like the other methods, and keep business
  rules in the `internal/<area>` Service, not in `app.go`.
- Long operations that the UI can cancel go through `a.imports` (importguard.go)
  and report progress with a Wails event.

## 2. Regenerate the bindings

```
go tool wails generate module
```

Commit the resulting `frontend/wailsjs` changes. Never hand-edit that
directory (a hook blocks it). If Smart App Control blocks `wailsbindings.exe`,
tell the user; CI's `windows` job will show the expected diff.

## 3. Update the e2e stub backend

`frontend/e2e/stub-backend.js` replaces the Go backend in UI tests
(`window.go.main.App`). Add or change the matching `async` method on `api` so
it behaves like the real one on the in-memory data, including emitting any
events the method emits (`emit('import-progress', n)` etc.). Stub data
includes HTML-laden names on purpose; keep it that way.

## 4. Wire up and verify

- Call the binding from `frontend/src` with `async`/`await` and `try`/`catch`.
  Build DOM with `h()`/`append` from `dom.ts`.
- Run:
  ```
  gofmt -l .
  go vet ./internal/... ./cmd/...
  go test ./internal/... ./cmd/...
  cd frontend && npm run test:e2e
  ```
- `git diff --stat -- frontend/wailsjs` should show the binding change alongside
  `app.go`.
