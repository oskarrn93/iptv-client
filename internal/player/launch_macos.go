package player

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// playlistTTL is how long the playlist file handed to VLC is kept. VLC reads
// it after open(1) returns, possibly while still starting up.
const playlistTTL = time.Minute

// macVLCCommand reuses a running VLC. Starting VLC's binary always opens a
// new instance and macOS VLC has no --one-instance, so VLC is sent a
// one-entry playlist through open(1) instead: Launch Services hands it to the
// running VLC, or starts one. The playlist carries the title and options,
// which `open --args` would drop for a running VLC.
func macVLCCommand(bundle, title, streamURL string, opts map[string]string) (*exec.Cmd, error) {
	name, err := writeTemp(vlcPlaylist(title, streamURL, opts))
	if err != nil {
		return nil, fmt.Errorf("writing playlist for VLC: %w", pathless(err))
	}
	time.AfterFunc(playlistTTL, func() { os.Remove(name) })
	return exec.Command("/usr/bin/open", "-a", bundle, name), nil
}

// writeTemp writes data to a new temp file and returns its name. The file
// holds the stream URL (often with credentials): CreateTemp makes it 0600 in
// the per-user temp dir.
func writeTemp(data string) (string, error) {
	f, err := os.CreateTemp("", "iptv-client-*.m3u")
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// vlcPlaylist returns a one-entry M3U. Its inputs have been validated and
// contain no line breaks, so they cannot add playlist lines.
func vlcPlaylist(title, streamURL string, opts map[string]string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	if title != "" {
		b.WriteString("#EXTINF:-1," + title + "\n")
	}
	for _, k := range slices.Sorted(maps.Keys(opts)) {
		b.WriteString("#EXTVLCOPT:" + k + "=" + opts[k] + "\n")
	}
	b.WriteString(streamURL + "\n")
	return b.String()
}

// pathless drops the path from a file error: callers log it, and the path
// contains the user name.
func pathless(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
