// Package macwin shows a memo-mcp server's pages in native macOS windows
// (NSWindow + WKWebView), on the AppKit run loop the menu bar already
// runs. It is a small Objective-C shim (macwin_darwin.m); navigation rules
// live in Go (internal/webpolicy). macOS only.
package macwin
