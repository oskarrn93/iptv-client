package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errImportRunning is returned when an import is already running.
var errImportRunning = errors.New("another playlist is being imported; wait for it to finish")

// errClosing is returned when an import is started while the app closes.
var errClosing = errors.New("the app is closing")

// importTimeout bounds a single add or refresh.
const importTimeout = 10 * time.Minute

// ImportEvent is emitted with an ImportState when an import starts and when
// it ends, whether the UI or the background sync started it.
const ImportEvent = "import"

// ImportState says which import is running.
type ImportState struct {
	Running    bool   `json:"running"`
	Op         string `json:"op"`         // the bound method's op, e.g. "refresh_playlist"
	PlaylistID int64  `json:"playlistId"` // 0 when adding a playlist
}

// importGuard allows one playlist import at a time and lets the UI cancel
// it. The zero value is ready to use and safe for concurrent use.
type importGuard struct {
	mu     sync.Mutex
	cancel context.CancelFunc // non-nil while an import runs
	closed bool
	// notify, if set, is called with each import's start and end, in order.
	notify func(ImportState)
}

// begin starts an import and returns its cancellable context and a func
// that must be called when the import ends.
func (g *importGuard) begin(parent context.Context, op string, playlistID int64) (context.Context, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case g.closed:
		return nil, nil, errClosing
	case g.cancel != nil:
		return nil, nil, errImportRunning
	}
	ctx, cancel := context.WithTimeout(parent, importTimeout)
	g.cancel = cancel
	state := ImportState{Running: true, Op: op, PlaylistID: playlistID}
	g.emit(state)
	return ctx, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		cancel()
		g.cancel = nil
		state.Running = false
		g.emit(state)
	}, nil
}

// emit notifies under g.mu, so that an import's end is never reported
// after the next one's start.
func (g *importGuard) emit(s ImportState) {
	if g.notify != nil {
		g.notify(s)
	}
}

// stop cancels the running import, if any.
func (g *importGuard) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
}

// close cancels the running import and refuses new ones.
func (g *importGuard) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	if g.cancel != nil {
		g.cancel()
	}
}
