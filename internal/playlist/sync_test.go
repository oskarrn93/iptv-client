package playlist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// syncCalls runs Sync with a step that records its calls as "id" or
// "id guide", and fails the items in fail.
func syncCalls(t *testing.T, s *Service, st *SyncState, fail map[string]bool, steps map[string]func()) string {
	t.Helper()
	var calls []string
	err := s.Sync(context.Background(), st, func(id int64, guide bool) (bool, error) {
		call := fmt.Sprint(id)
		if guide {
			call += " guide"
		}
		calls = append(calls, call)
		if f := steps[call]; f != nil {
			f()
		}
		if fail[call] {
			return false, errors.New("boom")
		}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(calls, ", ")
}

func TestSync(t *testing.T) {
	ctx := context.Background()
	svc := newServices(t)
	s := svc.Playlists
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	repo := s.repo
	create := func(typ SourceType, source string) int64 {
		id, err := repo.Create(ctx, "p", typ, source, now)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	stale := create(SourceURL, "http://a/") // 1: never refreshed, names a guide when refreshed
	file := create(SourceFile, "/a.m3u")    // 2: only its guide is synced
	disabled := create(SourceURL, "http://b/")
	fresh := create(SourceURL, "http://c/")
	must(t, repo.SetGuideURL(ctx, file, "http://g/"))
	must(t, repo.SetDisabled(ctx, disabled, true))
	must(t, repo.SetGuideURL(ctx, fresh, "http://g/"))
	must(t, repo.MarkRefreshed(ctx, fresh, now))
	must(t, repo.MarkGuideRefreshed(ctx, fresh, now))

	var st SyncState
	namesGuide := map[string]func(){fmt.Sprint(stale): func() {
		must(t, repo.SaveRefresh(ctx, stale, 0, "http://a/", "", "", "http://tvg/", now.Add(-48*time.Hour)))
	}}
	got := syncCalls(t, s, &st, map[string]bool{"2 guide": true}, namesGuide)
	if want := "1, 1 guide, 2 guide"; got != want {
		t.Fatalf("first sync = %q, want %q", got, want)
	}

	// The failed guide waits until it would be stale again.
	if got, want := syncCalls(t, s, &st, nil, nil), "1, 1 guide"; got != want {
		t.Errorf("sync after a failure = %q, want %q", got, want)
	}
	now = now.Add(GuideMaxAge)
	if got, want := syncCalls(t, s, &st, nil, nil), "1, 1 guide, 2 guide, 4 guide"; got != want {
		t.Errorf("sync once stale again = %q, want %q", got, want)
	}
}

func TestSyncStops(t *testing.T) {
	ctx := context.Background()
	s := newServices(t).Playlists
	for range 2 {
		if _, err := s.repo.Create(ctx, "p", SourceURL, "http://a/", s.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	var st SyncState
	err := s.Sync(ctx, &st, func(int64, bool) (bool, error) {
		calls++
		return true, context.Canceled
	})
	if err != nil || calls != 1 {
		t.Fatalf("calls = %d, err = %v; want 1 call", calls, err)
	}
	if len(st.failed) != 0 {
		t.Errorf("a stopped step was recorded as failed: %v", st.failed)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
