## Install

You also need [VLC](https://www.videolan.org/vlc/) — the app opens channels in it.

These builds are **not code-signed**, so your operating system will warn you the first time.

### Windows

1. Download `iptv-client-windows-amd64.zip`, extract it, and run `iptv-client.exe`.
2. If you see "Windows protected your PC", click **More info** → **Run anyway**.

On PCs with Smart App Control turned on, Windows may block the app entirely.

### macOS (Intel and Apple Silicon)

1. Download `iptv-client-macos-universal.zip`, open it, and move **iptv-client** to Applications.
2. Open it once. When macOS says it cannot verify the app, go to
   **System Settings → Privacy & Security** and click **Open Anyway**.

   Or, in Terminal:

   ```sh
   xattr -dr com.apple.quarantine /Applications/iptv-client.app
   ```

Your playlists are stored on your computer only. Playlist URLs often contain your
provider username and password and are saved unencrypted.
