package player

import "os/exec"

// command builds the command that plays a stream with the player at exe on
// goos. It validates its inputs, so callers cannot accidentally bypass the
// allowlists.
func command(goos, exe, title, streamURL string, opts map[string]string) (*exec.Cmd, error) {
	p, err := ForExecutable(exe)
	if err != nil {
		return nil, err
	}
	if err := ValidateURL(streamURL); err != nil {
		return nil, err
	}
	title, opts = cleanTitle(title), SanitizeOptions(opts)
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
