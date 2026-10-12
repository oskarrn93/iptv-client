package logging_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"iptv-client/internal/database"
	"iptv-client/internal/logging"
	"iptv-client/internal/player"
	"iptv-client/internal/playlist"
	"iptv-client/internal/setting"
)

// TestNoSecretsInLogFiles runs real failure paths with credential-laden
// URLs and home-directory paths, logs their errors the way App does, and
// checks the files on disk contain neither.
func TestNoSecretsInLogFiles(t *testing.T) {
	const secret = "Pa55w0rdXYZ"
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 4 {
		t.Skip("no usable home directory")
	}

	dir := t.TempDir()
	logs, err := logging.Setup(dir, logging.Meta{Version: "test", Run: "leak01"})
	if err != nil {
		t.Fatal(err)
	}
	logs.Level.Set(-8) // everything, below Debug
	ctx := context.Background()

	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := playlist.NewService(db, setting.NewService(db))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	fail := func(op string, err error) {
		if err == nil {
			t.Fatalf("%s: expected an error", op)
		}
		logs.Logger.WarnContext(logging.With(ctx, slog.String("op", op)), "call failed", "err", err)
	}
	// Import from a URL with credentials in user info, path and query.
	_, _, err = svc.Add(ctx, "", "http://user:"+secret+"@"+host+"/get.php?username=u&password="+secret, nil)
	fail("add_playlist", err)
	_, _, err = svc.Add(ctx, "", "http://"+host+"/live/user/"+secret+"/list.m3u", nil)
	fail("add_playlist", err)
	// A local playlist under the home directory that does not exist. File
	// names are kept; only the home directory (the user name) is hidden.
	_, _, err = svc.Add(ctx, "", filepath.Join(home, "no-such-dir", "list.m3u"), nil)
	fail("add_playlist", err)
	// An unplayable stream URL.
	fail("play", player.ValidateURL("http://user:"+secret+"@h:bad/x"))
	fail("play", player.ValidateURL("ftp://h/"+secret))
	// Wails' raw-call error after a panic in a bound method.
	logging.Wails(logs.Logger).Error(`process message error: C{"name":"main.App.AddPlaylistURL","args":["","http://u:` +
		secret + `@provider.tv/x"],"callbackID":"1"} -> boom`)
	// A frontend error with a URL in its message.
	logging.NewUI(logs.Logger).Log("error", "uncaught error", map[string]string{"message": "fetch http://" + host + "/" + secret + " failed"})
	logs.Logger.Info("direct", "path", filepath.Join(home, "Documents", "list.m3u"))

	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) == 0 {
		t.Fatal("no log files written")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out := string(b)
		for _, leak := range []string{secret, home, filepath.ToSlash(home), strings.ReplaceAll(home, `\`, `\\`)} {
			if strings.Contains(out, leak) {
				t.Errorf("%s contains %q:\n%s", filepath.Base(f), leak, out)
			}
		}
		if !strings.Contains(out, "op=add_playlist") || !strings.Contains(out, "component=wails") || !strings.Contains(out, "component=ui") {
			t.Errorf("%s is missing expected lines:\n%s", filepath.Base(f), out)
		}
	}
}
