//go:build !darwin

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"iptv-client/internal/setting"
)

// setWindowTheme sets the title bar's theme. The web view keeps following
// the system, so the UI applies the theme with CSS.
func setWindowTheme(ctx context.Context, theme string) {
	switch theme {
	case setting.ThemeLight:
		runtime.WindowSetLightTheme(ctx)
	case setting.ThemeDark:
		runtime.WindowSetDarkTheme(ctx)
	default:
		runtime.WindowSetSystemDefaultTheme(ctx)
	}
}
