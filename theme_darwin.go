package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void setAppearance(int theme) {
	dispatch_async(dispatch_get_main_queue(), ^{
		switch (theme) {
		case 1: NSApp.appearance = [NSAppearance appearanceNamed:NSAppearanceNameAqua]; break;
		case 2: NSApp.appearance = [NSAppearance appearanceNamed:NSAppearanceNameDarkAqua]; break;
		default: NSApp.appearance = nil;
		}
	});
}
*/
import "C"

import (
	"context"

	"iptv-client/internal/setting"
)

// setWindowTheme sets the app's appearance. Wails' WindowSet*Theme does
// nothing on macOS. The web view follows the appearance, so
// prefers-color-scheme matches the theme too.
func setWindowTheme(_ context.Context, theme string) {
	var t C.int
	switch theme {
	case setting.ThemeLight:
		t = 1
	case setting.ThemeDark:
		t = 2
	}
	C.setAppearance(t)
}
