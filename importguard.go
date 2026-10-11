package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// importTimeout bounds a single add or refresh.
const importTimeout = 10 * time.Minute

// importGuard allows one playlist import at a time and lets the UI cancel
// it. The zero value is ready to use and safe for concurrent use.
type importGuard struct {
	mu     sync.Mutex
	cancel context.CancelFunc // non-nil while an import runs
}

// begin starts an import and returns its cancellable context and a func
// that must be called when the import ends.
func (g *importGuard) begin(parent context.Context) (context.Context, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		return nil, nil, errors.New("another playlist is being imported; wait for it to finish")
	}
	ctx, cancel := context.WithTimeout(parent, importTimeout)
	g.cancel = cancel
	return ctx, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		cancel()
		g.cancel = nil
	}, nil
}

// stop cancels the running import, if any.
func (g *importGuard) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
}
