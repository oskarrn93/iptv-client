package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#import <Cocoa/Cocoa.h>

static void finishAppMenu(const char *hideTitle) {
	NSString *hide = [NSString stringWithUTF8String:hideTitle];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSMenu *bar = [NSApp mainMenu];
		if (bar.numberOfItems == 0) return;
		NSMenu *app = [bar itemAtIndex:0].submenu;
		NSInteger i = [app indexOfItemWithTitle:hide];
		// Not found, or already done (OnDomReady runs again on reload).
		if (i < 0 || [app indexOfItemWithTitle:@"Hide Others"] >= 0) return;

		NSMenu *services = [[[NSMenu alloc] initWithTitle:@"Services"] autorelease];
		NSMenuItem *servicesItem = [[[NSMenuItem alloc] initWithTitle:@"Services" action:nil keyEquivalent:@""] autorelease];
		servicesItem.submenu = services;
		[NSApp setServicesMenu:services];
		[app insertItem:servicesItem atIndex:i];
		[app insertItem:[NSMenuItem separatorItem] atIndex:i + 1];

		// A nil target sends these to NSApp through the responder chain.
		NSMenuItem *others = [[[NSMenuItem alloc] initWithTitle:@"Hide Others"
			action:@selector(hideOtherApplications:) keyEquivalent:@"h"] autorelease];
		others.keyEquivalentModifierMask = NSEventModifierFlagOption | NSEventModifierFlagCommand;
		[app insertItem:others atIndex:i + 3];
		[app insertItem:[[[NSMenuItem alloc] initWithTitle:@"Show All"
			action:@selector(unhideAllApplications:) keyEquivalent:@""] autorelease] atIndex:i + 4];

		// Gives the Help menu its search field.
		NSInteger help = [bar indexOfItemWithTitle:@"Help"];
		if (help >= 0) [NSApp setHelpMenu:[bar itemAtIndex:help].submenu];
	});
}
*/
import "C"

import (
	"context"
	"unsafe"
)

// finishAppMenu adds the standard app menu items that Wails cannot create.
// It runs once the window has loaded, after Wails has installed the menu.
func finishAppMenu(context.Context) {
	title := C.CString(hideLabel)
	defer C.free(unsafe.Pointer(title))
	C.finishAppMenu(title)
}
