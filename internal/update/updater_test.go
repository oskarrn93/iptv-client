package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakePlatform downloads the Windows asset, records the install and returns
// err from it.
type fakePlatform struct {
	installed string
	err       error
}

func (*fakePlatform) AssetName() string { return ForOS("windows").AssetName() }

func (f *fakePlatform) Install(path string) error {
	f.installed = path
	return f.err
}

func TestUpdaterDevBuild(t *testing.T) {
	u := &Updater{Checker: Checker{APIBase: "http://unreachable.invalid"}, Current: "dev"}
	if _, _, err := u.Check(context.Background()); !errors.Is(err, ErrDevBuild) {
		t.Fatalf("Check = %v, want ErrDevBuild", err)
	}
}

func TestUpdaterFailedCheckForgetsRelease(t *testing.T) {
	payload := []byte("installer bytes")
	srv := server(t, payload, "sha256:"+sum(payload))
	u := &Updater{
		Checker: Checker{APIBase: srv.URL, Client: srv.Client(), Platform: &fakePlatform{}},
		Current: "v1.3.0",
	}
	if _, newer, err := u.Check(context.Background()); err != nil || !newer {
		t.Fatalf("Check = %v, %v", newer, err)
	}
	srv.Close()
	if _, _, err := u.Check(context.Background()); err == nil {
		t.Fatal("Check against a closed server succeeded")
	}
	if err := u.Install(context.Background(), nil); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("Install after a failed Check: %v, want ErrNoUpdate", err)
	}
}

func TestUpdaterInstallFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kept bool
	}{
		{"failed", errors.New("boom"), false},
		{"manual", fmt.Errorf("%w: do it yourself", errManual), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte("installer bytes")
			srv := server(t, payload, "sha256:"+sum(payload))
			p := &fakePlatform{err: tc.err}
			u := &Updater{
				Checker: Checker{APIBase: srv.URL, Client: srv.Client(), Platform: p},
				Current: "v1.3.0",
			}
			if _, _, err := u.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := u.Install(context.Background(), nil); !errors.Is(err, tc.err) {
				t.Fatalf("Install = %v, want %v", err, tc.err)
			}
			t.Cleanup(func() { os.RemoveAll(filepath.Dir(p.installed)) })
			if _, err := os.Stat(p.installed); (err == nil) != tc.kept {
				t.Errorf("download kept = %v, want %v", err == nil, tc.kept)
			}
			// A failed install can be retried.
			if err := u.Install(context.Background(), nil); errors.Is(err, ErrInstalling) {
				t.Error("retry refused with ErrInstalling")
			}
		})
	}
}

func TestUpdaterInstallRequiresNewerRelease(t *testing.T) {
	payload := []byte("installer bytes")
	srv := server(t, payload, "sha256:"+sum(payload))
	u := &Updater{
		Checker: Checker{APIBase: srv.URL, Client: srv.Client(), Platform: &fakePlatform{}},
		Current: "v1.4.0",
	}
	if err := u.Install(context.Background(), nil); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("Install before Check: %v, want ErrNoUpdate", err)
	}
	if _, newer, err := u.Check(context.Background()); err != nil || newer {
		t.Fatalf("Check = %v, %v; want not newer", newer, err)
	}
	if err := u.Install(context.Background(), nil); !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("Install when up to date: %v, want ErrNoUpdate", err)
	}
}

func TestUpdaterInstall(t *testing.T) {
	payload := []byte("installer bytes")
	srv := server(t, payload, "sha256:"+sum(payload))
	p := &fakePlatform{}
	u := &Updater{
		Checker: Checker{APIBase: srv.URL, Client: srv.Client(), Platform: p},
		Current: "v1.3.0",
	}
	r, newer, err := u.Check(context.Background())
	if err != nil || !newer || r.Version != "v1.4.0" {
		t.Fatalf("Check = %+v, %v, %v", r, newer, err)
	}
	if err := u.Install(context.Background(), func(int) {}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(p.installed)) })
	if got, err := os.ReadFile(p.installed); err != nil || string(got) != string(payload) {
		t.Fatalf("installed %q, %v", got, err)
	}
	// The app is quitting; a second install must not start another installer.
	if err := u.Install(context.Background(), nil); !errors.Is(err, ErrInstalling) {
		t.Errorf("second Install = %v, want ErrInstalling", err)
	}
}
