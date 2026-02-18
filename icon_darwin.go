//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

void setDockIcon(const void* data, int length) {
	@autoreleasepool {
		NSData* imgData = [NSData dataWithBytes:data length:(NSUInteger)length];
		NSImage* img = [[NSImage alloc] initWithData:imgData];
		if (img) {
			[[NSApplication sharedApplication] setApplicationIconImage:img];
		}
	}
}
*/
import "C"

import "unsafe"

func setAppIcon() {
	png := getAppIconPNG()
	if len(png) == 0 {
		return
	}
	C.setDockIcon(unsafe.Pointer(&png[0]), C.int(len(png)))
}
