# Developer setup

How to get from a clean machine to a running build of IPTV Client. Every step
below was run on the reference Windows machine; the macOS steps follow the
same toolchain but have not yet been verified on a Mac (see
[Status](#status)).

## What you need

| Tool | Version used | Why |
|---|---|---|
| Git | any recent | source control |
| Go | 1.27.0 (go.mod requires ≥ 1.26) | backend, build tool |
| Node.js | 24 LTS (24.20.0), with npm 11 | builds the frontend (Vite + TypeScript) |
| Wails CLI | v2.16.0 | **not installed globally** — pinned in `go.mod` and run with `go tool wails` |
| WebView2 runtime | preinstalled on Windows 10/11 | renders the UI on Windows |
| Xcode Command Line Tools | macOS only | C compiler for the macOS webview (CGO) |
| VLC | 3.x | needed to actually play streams |

No C compiler is needed on Windows. SQLite is the pure-Go `modernc.org/sqlite`
driver, so there is no CGO there either.

## Windows

Open PowerShell (no admin prompt needed; the installers ask for elevation themselves).

```powershell
# 1. Toolchain
winget install --id Git.Git -e
winget install --id GoLang.Go -e
winget install --id OpenJS.NodeJS.LTS -e
winget install --id VideoLAN.VLC -e        # to play streams

# 2. Open a NEW terminal so PATH picks up Go and Node, then check
go version        # go1.27.0 or newer
node --version    # v24.x
npm --version

# 3. Get the code
git clone <repo-url> iptv-client
cd iptv-client

# 4. Check the Wails toolchain (downloads the pinned CLI on first run)
go tool wails doctor     # should end with "Your system is ready for Wails development!"
```

### Smart App Control (Windows 11)

If Smart App Control is **on** (Windows Security → App & browser control →
Smart App Control), Windows blocks unsigned executables with no download
reputation — and everything you compile locally is exactly that. On the
reference machine this showed up as:

```
An Application Control policy has blocked this file
```

What we observed:

- `go install …/wails` produced a `wails.exe` that was **blocked**. That is why
  the CLI is pinned as a Go tool and run via `go tool wails` (which works).
- `go test` and the built `build/bin/iptv-client.exe` ran fine.
- The **fuzz** test binary (`go test -fuzz`) was blocked. Fuzzing runs in CI on
  Linux instead.

Smart App Control decides per binary, so other builds may be hit. Options if
something you build is blocked: run that step in CI, or turn Smart App Control
off. Note that turning it off is permanent unless you reinstall Windows, so
don't do that casually.

Check its state from PowerShell (`1` = on, `0` = off, `2` = evaluation):

```powershell
(Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy').VerifiedAndReputablePolicyState
```

## macOS

```bash
# 1. Toolchain
xcode-select --install                 # C compiler for CGO (webview)
brew install go node git
brew install --cask vlc                # to play streams

# 2. Check
go version && node --version

# 3. Get the code and check Wails
git clone <repo-url> iptv-client && cd iptv-client
go tool wails doctor
```

## Everyday commands

Run from the repository root.

| Task | Command |
|---|---|
| Run the app with hot reload | `go tool wails dev` |
| Production build (current OS) | `go tool wails build -clean -trimpath` → `build/bin/` |
| macOS universal build | `go tool wails build -clean -trimpath -platform darwin/universal` |
| Unit tests (core) | `go test ./internal/... ./cmd/...` |
| Skip the 200k-row performance test | `go test -short ./internal/...` |
| Fuzz the parser (Linux/macOS) | `go test ./internal/m3u -run '^$' -fuzz FuzzParse -fuzztime 60s` |
| Format check | `gofmt -l cmd internal` (should print nothing) |
| Debug CLI (no GUI) | `go run ./cmd/iptv-debug -h` |

Do not use `-upx`: compressed binaries are a common antivirus false-positive trigger.

### Debug CLI

`cmd/iptv-debug` drives the same core library as the GUI. It is a dev tool
only and is not shipped. Use `-db` to keep experiments out of your real
database:

```bash
go run ./cmd/iptv-debug -db ./scratch.db add https://example.com/playlist.m3u "My list"
go run ./cmd/iptv-debug -db ./scratch.db groups 1
go run ./cmd/iptv-debug -db ./scratch.db search 1 bbc
go run ./cmd/iptv-debug -db ./scratch.db play 42
go run ./cmd/iptv-debug vlc        # which VLC would be used
```

## Where data lives

| OS | Database |
|---|---|
| Windows | `%AppData%\IPTVClient\iptv.db` |
| macOS | `~/Library/Application Support/IPTVClient/iptv.db` |

Playlist URLs (which often contain your provider username and password) are
stored **unencrypted** in this file.

## CI and releases

`.github/workflows/ci.yml`:

- **Every push / PR:** format check, vet, race-enabled tests and 60 s of
  fuzzing on Linux; Windows build uploaded as an artifact.
- **Tags `v*` (or manual run):** additionally a macOS universal build,
  ad-hoc signed, and a GitHub Release with both zips.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## Running unsigned builds (for end users)

The app is not code-signed (no paid developer account).

- **Windows:** SmartScreen shows "Windows protected your PC" → **More info** →
  **Run anyway**. On machines with Smart App Control enabled the app may be
  blocked outright.
- **macOS:** a downloaded build is quarantined. Either open it once, then go to
  **System Settings → Privacy & Security → Open Anyway**, or run:
  ```bash
  xattr -dr com.apple.quarantine /Applications/iptv-client.app
  ```
  Builds you compile yourself on the same Mac are not quarantined.

## Status

| Step | Windows 11 (25H2) | macOS |
|---|---|---|
| Toolchain install | ✅ verified 2026-10-11 | ⏳ not yet verified |
| `go tool wails doctor` | ✅ | ⏳ |
| Tests | ✅ | ⏳ (CI) |
| App build + window opens | ✅ | ⏳ (CI on tags) |
| VLC detection + playback | ✅ (registry lookup) | ⏳ |

Update this table when you verify a step on a new platform.
