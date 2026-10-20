package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"iptv-client/internal/epg"
	"iptv-client/internal/logo"
)

// serveLogo serves channel logos at /logo/<channel id> for the asset
// server, from the logo cache. The UI only ever sees this path: logo URLs
// come from the playlist or guide and may carry credentials.
func (a *App) serveLogo(w http.ResponseWriter, r *http.Request) {
	idText, ok := strings.CutPrefix(r.URL.Path, "/logo/")
	id, err := strconv.ParseInt(idText, 10, 64)
	svc, readyErr := a.ready()
	if !ok || err != nil || r.Method != http.MethodGet || readyErr != nil || a.logos == nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	c, err := svc.Channels.Get(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ua, err := svc.Settings.UserAgent(ctx)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	img, err := a.logos.Get(ctx, c.TvgLogo, ua)
	if errors.Is(err, logo.ErrNoLogo) {
		// The guide icon is only a fallback, so look it up only when needed.
		guide, gerr := svc.Guide.Current(ctx, []epg.Ref{c.GuideRef()})
		if gerr == nil {
			img, err = a.logos.Get(ctx, guide[id].Icon, ua)
		}
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", img.Type)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=86400")
	w.Write(img.Data)
}
