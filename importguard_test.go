package main

import (
	"context"
	"errors"
	"testing"
)

func TestImportGuardClose(t *testing.T) {
	var g importGuard
	var states []ImportState
	g.notify = func(s ImportState) { states = append(states, s) }

	ctx, done, err := g.begin(context.Background(), "refresh_playlist", 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.begin(context.Background(), "add_playlist", 0); !errors.Is(err, errImportRunning) {
		t.Fatalf("second begin = %v, want errImportRunning", err)
	}

	g.close()
	if ctx.Err() == nil {
		t.Error("close did not cancel the running import")
	}
	done()
	if _, _, err := g.begin(context.Background(), "add_playlist", 0); !errors.Is(err, errClosing) {
		t.Errorf("begin after close = %v, want errClosing", err)
	}

	want := []ImportState{
		{Running: true, Op: "refresh_playlist", PlaylistID: 7},
		{Running: false, Op: "refresh_playlist", PlaylistID: 7},
	}
	if len(states) != len(want) || states[0] != want[0] || states[1] != want[1] {
		t.Errorf("states = %+v, want %+v", states, want)
	}
}
