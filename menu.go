package main

import (
	_ "embed"
	goruntime "runtime"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Events that ask the UI to run an action chosen in the app menu.
const (
	MenuSettingsEvent    = "menu-settings"
	MenuCheckUpdateEvent = "menu-check-update"
)

const (
	appName     = "IPTV Client"
	hideLabel   = "Hide " + appName // finishAppMenu inserts native items around it
	homepageURL = "https://github.com/oskarrn93/iptv-client"
)

//go:embed build/appicon.png
var appIcon []byte

// appMenu returns the macOS menu bar, or nil elsewhere: Windows has no app
// menu, and a menu bar would add a strip to the window.
//
// The app menu is built by hand because Wails' AppMenu role cannot take
// extra items such as Settings and Check for Updates. macOS shows the first
// submenu as the app menu, titled with the app's name. Items Wails cannot
// create (Services, Hide Others, Show All) are added by finishAppMenu.
func (a *App) appMenu() *menu.Menu {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	emit := func(event string) menu.Callback {
		return func(*menu.CallbackData) { runtime.EventsEmit(a.ctx, event) }
	}
	app := menu.NewMenu()
	app.AddText("About "+appName, nil, func(*menu.CallbackData) { a.about() })
	app.AddText("Check for Updates…", nil, emit(MenuCheckUpdateEvent))
	app.AddSeparator()
	app.AddText("Settings…", keys.CmdOrCtrl(","), emit(MenuSettingsEvent))
	app.AddSeparator()
	app.AddText(hideLabel, keys.CmdOrCtrl("h"), func(*menu.CallbackData) { runtime.Hide(a.ctx) })
	app.AddSeparator()
	app.AddText("Quit "+appName, keys.CmdOrCtrl("q"), func(*menu.CallbackData) { runtime.Quit(a.ctx) })

	help := menu.NewMenu()
	help.AddText(appName+" on GitHub", nil, func(*menu.CallbackData) { runtime.BrowserOpenURL(a.ctx, homepageURL) })
	// Its error is logged by the method itself.
	help.AddText("Open Log Folder…", nil, func(*menu.CallbackData) { _ = a.OpenLogFolder() })

	// The Edit menu is what makes copy and paste work in text fields.
	return menu.NewMenuFromItems(menu.SubMenu(appName, app), menu.EditMenu(), menu.WindowMenu(), menu.SubMenu("Help", help))
}

// about shows the version in a native dialog.
func (a *App) about() {
	_, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:  runtime.InfoDialog,
		Title: appName,
		Message: "Version " + version + "\n\n" +
			"Browse M3U playlists and play channels in VLC, mpv or IINA.\n\n" +
			homepageURL,
		Icon: appIcon,
	})
	if err != nil {
		a.log.WarnContext(a.ctx, "cannot show the about dialog", "err", err)
	}
}
