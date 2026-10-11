# IPTV Client

A desktop app for Windows and macOS that imports M3U/M3U8 IPTV playlists,
lets you browse them by group, search them, and opens channels in
[VLC](https://www.videolan.org).

Built with Go, [Wails](https://wails.io) (native window and system webview)
and SQLite.

> **Status:** core library, debug CLI and GUI are working on Windows; macOS
> builds are produced by CI but not yet verified on a Mac.

## Features

- Multiple playlists, from a URL or a local file
- Browse by `group-title`, full-text search, favorites
- Favorites survive playlist refreshes, even if the provider changes the
  credentials embedded in stream URLs
- Handles very large playlists (200k channels import in about 3 seconds)
- Plays in VLC, passing the channel name and any allowed `#EXTVLCOPT`
  headers (User-Agent, Referrer)

## Security model

Playlists are untrusted input. Before anything reaches VLC:

- Stream URLs must use `http`, `https`, `rtmp(s)`, `rtsp`, `rtp`, `udp` or
  `mms(h)` — no `file:`, no option-like values.
- Only `http-user-agent`, `http-referrer` and `network-caching` are taken
  from `#EXTVLCOPT`; options such as `sout` are dropped.
- VLC is started directly (never through a shell), and its path must point
  to a `vlc`/`vlc.exe` executable.

Playlist URLs are stored unencrypted in the local database.

## Layout

```
main.go, app.go        Wails entry point and GUI bindings
frontend/              UI (Vite + TypeScript, no framework)
frontend/e2e           UI tests against a stubbed backend
internal/m3u           streaming playlist parser
internal/store         SQLite storage, search, favorites
internal/fetch         playlist download (size cap, ETag, credential-safe errors)
internal/player        VLC discovery, URL/option allowlists, launch
internal/library       service used by GUI and CLI (import, refresh, play)
cmd/iptv-debug         developer CLI (not shipped)
docs/SETUP.md          developer setup — start here
```

## Getting started

See **[docs/SETUP.md](docs/SETUP.md)** for installing the toolchain, building,
testing and running unsigned builds.
