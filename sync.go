package main

import (
	"context"
	"errors"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"iptv-client/internal/playlist"
)

// syncInterval is how often the background sync looks for stale playlists
// and guides.
const syncInterval = time.Hour

// SyncEvent is emitted with a SyncUpdate when a background refresh ends.
// While it runs, ImportEvent and ImportProgressEvent report it like any
// import, and CancelImport stops it.
const SyncEvent = "sync"

// SyncUpdate reports the result of a background refresh to the UI.
type SyncUpdate struct {
	PlaylistID int64  `json:"playlistId"`
	Guide      bool   `json:"guide"`   // the program guide, not the channels
	Changed    bool   `json:"changed"` // new data was stored
	Error      string `json:"error"`   // why it failed, "" on success
}

// domReady runs when the page has loaded, also after a reload.
func (a *App) domReady(ctx context.Context) {
	finishAppMenu(ctx)
	// The UI follows the sync through events, so start it only once the
	// page listens.
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.svc != nil && a.stopSync == nil {
		a.stopSync = a.startSync(ctx)
	}
}

// startSync runs the background sync until the returned func is called,
// which waits for it to end. Close the import guard first, so that a
// running refresh ends too.
func (a *App) startSync(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var st playlist.SyncState
		t := time.NewTicker(syncInterval)
		defer t.Stop()
		for {
			if err := a.svc.Playlists.Sync(ctx, &st, a.syncStep); err != nil && ctx.Err() == nil {
				a.log.WarnContext(ctx, "sync failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// syncStep is the background sync's playlist.SyncStep: it refreshes like
// the UI does, through the bound methods, and reports the result with
// SyncEvent.
func (a *App) syncStep(id int64, guide bool) (stop bool, err error) {
	var changed bool
	if guide {
		_, err = a.RefreshGuide(id)
		changed = err == nil
	} else {
		var res ImportResult
		res, err = a.RefreshPlaylist(id, false)
		changed = err == nil && res.Playlist != nil && !res.NotModified // nil: refused as too soon
	}
	// The user is importing, or the app is closing: try again next time.
	if errors.Is(err, errImportRunning) || errors.Is(err, errClosing) {
		return true, nil
	}
	// The guide URL was removed after the sync picked the item: not a failure.
	if errors.Is(err, playlist.ErrNoGuide) {
		return false, nil
	}
	u := SyncUpdate{PlaylistID: id, Guide: guide, Changed: changed}
	if err != nil {
		u.Error = err.Error()
	}
	runtime.EventsEmit(a.ctx, SyncEvent, u)
	return errors.Is(err, context.Canceled), err
}
