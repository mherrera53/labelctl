//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdlib.h>

// ─── Window delegate: hide on close (don't destroy) ───
@interface BridgeWindowDelegate : NSObject <NSWindowDelegate>
@end

@implementation BridgeWindowDelegate
- (BOOL)windowShouldClose:(NSWindow *)sender {
	// Hide the window instead of closing it
	[sender orderOut:nil];
	// Switch back to menu-bar-only mode (no dock icon)
	[[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyAccessory];
	return NO;
}
@end

// ─── WKUIDelegate: handle JS alert(), confirm(), prompt() in WKWebView ───
@interface BridgeUIDelegate : NSObject <WKUIDelegate>
@end

@implementation BridgeUIDelegate
- (void)webView:(WKWebView *)webView runJavaScriptAlertPanelWithMessage:(NSString *)message
        initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(void))completionHandler {
	NSAlert *alert = [[NSAlert alloc] init];
	[alert setMessageText:message];
	[alert addButtonWithTitle:@"OK"];
	[alert runModal];
	completionHandler();
}

- (void)webView:(WKWebView *)webView runJavaScriptConfirmPanelWithMessage:(NSString *)message
        initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))completionHandler {
	NSAlert *alert = [[NSAlert alloc] init];
	[alert setMessageText:message];
	[alert addButtonWithTitle:@"OK"];
	[alert addButtonWithTitle:@"Cancelar"];
	NSModalResponse response = [alert runModal];
	completionHandler(response == NSAlertFirstButtonReturn);
}

- (void)webView:(WKWebView *)webView runJavaScriptTextInputPanelWithPrompt:(NSString *)prompt
        defaultText:(NSString *)defaultText initiatedByFrame:(WKFrameInfo *)frame
        completionHandler:(void (^)(NSString *))completionHandler {
	NSAlert *alert = [[NSAlert alloc] init];
	[alert setMessageText:prompt];
	[alert addButtonWithTitle:@"OK"];
	[alert addButtonWithTitle:@"Cancelar"];
	NSTextField *input = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 200, 24)];
	[input setStringValue:defaultText ?: @""];
	[alert setAccessoryView:input];
	NSModalResponse response = [alert runModal];
	if (response == NSAlertFirstButtonReturn) {
		completionHandler([input stringValue]);
	} else {
		completionHandler(nil);
	}
}
@end

static NSWindow *bridgeWindow = nil;
static WKWebView *bridgeWebView = nil;
static BridgeWindowDelegate *bridgeDelegate = nil;
static BridgeUIDelegate *bridgeUIDelegate = nil;

// nativeCreateWindow creates an NSWindow with WKWebView and shows it.
// Safe to call from any thread — dispatches to main queue.
void nativeCreateWindow(const char* title, const char* url, int width, int height) {
	NSString *nsTitle = [NSString stringWithUTF8String:title];
	NSString *nsURL   = [NSString stringWithUTF8String:url];

	dispatch_async(dispatch_get_main_queue(), ^{
		if (bridgeWindow) {
			// Window exists — just navigate and show
			NSURL *u = [NSURL URLWithString:nsURL];
			[bridgeWebView loadRequest:[NSURLRequest requestWithURL:u]];
			[bridgeWindow setTitle:nsTitle];
			[[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyRegular];
			[bridgeWindow makeKeyAndOrderFront:nil];
			[[NSApplication sharedApplication] activateIgnoringOtherApps:YES];
			return;
		}

		NSRect frame = NSMakeRect(0, 0, width, height);
		NSUInteger style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
		                   NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;

		bridgeWindow = [[NSWindow alloc] initWithContentRect:frame
		                                 styleMask:style
		                                 backing:NSBackingStoreBuffered
		                                 defer:NO];
		[bridgeWindow setTitle:nsTitle];
		[bridgeWindow center];
		[bridgeWindow setReleasedWhenClosed:NO];

		// Set delegate to handle close button (hide instead of destroy)
		bridgeDelegate = [[BridgeWindowDelegate alloc] init];
		[bridgeWindow setDelegate:bridgeDelegate];

		// Create WKWebView with UI delegate for JS dialogs (alert, confirm, prompt)
		WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
		bridgeWebView = [[WKWebView alloc] initWithFrame:frame configuration:config];
		[bridgeWebView setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
		bridgeUIDelegate = [[BridgeUIDelegate alloc] init];
		[bridgeWebView setUIDelegate:bridgeUIDelegate];
		[bridgeWindow setContentView:bridgeWebView];

		// Navigate
		NSURL *u = [NSURL URLWithString:nsURL];
		[bridgeWebView loadRequest:[NSURLRequest requestWithURL:u]];

		// Show window and activate app (shows dock icon)
		[[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyRegular];
		[bridgeWindow makeKeyAndOrderFront:nil];
		[[NSApplication sharedApplication] activateIgnoringOtherApps:YES];
	});
}

// nativeShowWindow re-shows a hidden window and optionally navigates.
void nativeShowWindow(const char* url) {
	NSString *nsURL = url ? [NSString stringWithUTF8String:url] : nil;

	dispatch_async(dispatch_get_main_queue(), ^{
		if (!bridgeWindow) return;

		if (nsURL) {
			NSURL *u = [NSURL URLWithString:nsURL];
			[bridgeWebView loadRequest:[NSURLRequest requestWithURL:u]];
		}

		[[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyRegular];
		[bridgeWindow makeKeyAndOrderFront:nil];
		[[NSApplication sharedApplication] activateIgnoringOtherApps:YES];
	});
}

// nativeSetWindowTitle updates the window title.
void nativeSetWindowTitle(const char* title) {
	NSString *nsTitle = [NSString stringWithUTF8String:title];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (bridgeWindow) {
			[bridgeWindow setTitle:nsTitle];
		}
	});
}

// nativeDestroyWindow closes and releases the window.
void nativeDestroyWindow() {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (bridgeWindow) {
			[bridgeWindow setDelegate:nil];
			[bridgeWindow close];
			bridgeWindow = nil;
			bridgeWebView = nil;
			bridgeDelegate = nil;
			[[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyAccessory];
		}
	});
}

// nativeIsWindowVisible returns 1 if the window exists and is visible.
int nativeIsWindowVisible() {
	if (bridgeWindow && [bridgeWindow isVisible]) return 1;
	return 0;
}

// nativeIsWindowCreated returns 1 if the window has been created (visible or hidden).
int nativeIsWindowCreated() {
	return bridgeWindow != nil ? 1 : 0;
}
*/
import "C"

import (
	"fmt"
	"log"
	"sync"
	"unsafe"
)

var (
	wvMu           sync.Mutex
	wvDashboardURL string
	wvCreated      bool
)

// initWebview stores the dashboard URL. On macOS, the native window is created
// lazily in showDashboard() AFTER systray.Run() starts the Cocoa event loop.
// This avoids the NSApplication conflict between webview_go and systray.
func initWebview(dashURL string) {
	wvMu.Lock()
	defer wvMu.Unlock()
	wvDashboardURL = dashURL
	log.Printf("[webview] macOS native mode — window will be created on first show")
}

// showDashboard creates or shows the native WKWebView window.
// Creates the window on first call, re-shows on subsequent calls.
func showDashboard(dashURL string) {
	wvMu.Lock()
	url := dashURL
	if url == "" {
		url = wvDashboardURL
	}
	created := wvCreated
	wvMu.Unlock()

	if url == "" {
		return
	}

	cfg := getConfig()
	title := "TSC Bridge"
	if cfg.Whitelabel.Name != "" {
		title = fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}

	cTitle := C.CString(title)
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cURL))

	if !created {
		log.Printf("[webview] creating native window: %s", url)
		C.nativeCreateWindow(cTitle, cURL, 1100, 750)
		wvMu.Lock()
		wvCreated = true
		wvMu.Unlock()
	} else {
		log.Printf("[webview] showing native window: %s", url)
		C.nativeShowWindow(cURL)
	}
}

// setWebviewTitle updates the window title with whitelabel branding.
func setWebviewTitle() {
	cfg := getConfig()
	title := "TSC Bridge"
	if cfg.Whitelabel.Name != "" {
		title = fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}

	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	C.nativeSetWindowTitle(cTitle)
}

// isWebviewActive reports whether the native window is visible.
func isWebviewActive() bool {
	return C.nativeIsWindowCreated() == 1
}

// destroyWebview tears down the native window.
func destroyWebview() {
	log.Printf("[webview] destroying native window")
	C.nativeDestroyWindow()
	wvMu.Lock()
	wvCreated = false
	wvMu.Unlock()
}
