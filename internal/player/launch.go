package player

import "os/exec"

// command builds the command that plays a stream with the player at exe on
// goos. It validates its inputs, so callers cannot accidentally bypass the
// allowlists.
func command(goos, exe, title, streamURL string, opts map[string]string, prefs Prefs) (*exec.Cmd, error) {
	p, err := ForExecutable(exe)
	if err != nil {
		return nil, err
	}
	if err := ValidateURL(streamURL); err != nil {
		return nil, err
	}
	langs, err := NormalizeLanguages(prefs.AudioLanguages)
	if err != nil {
		return nil, err
	}
	title, opts = cleanTitle(title), SanitizeOptions(opts)
	// Added after sanitizing: playlists cannot set it, only the user. The
	// profiles map it like a VLC option.
	if langs != "" {
		if opts == nil {
			opts = map[string]string{}
		}
		opts["audio-language"] = langs
	}
	args := p.args(title, streamURL, opts)
	if _, isVLC := p.(vlc); isVLC {
		switch goos {
		case "windows":
			// Hands the stream to an already running VLC instead of opening
			// another window.
			args = append([]string{"--one-instance"}, args...)
		case "darwin":
			if bundle, ok := appBundle(exe); ok {
				return macVLCCommand(bundle, title, streamURL, opts)
			}
		}
	}
	return exec.Command(exe, args...), nil
}
