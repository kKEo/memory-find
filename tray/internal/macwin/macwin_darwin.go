//go:build darwin

package macwin

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#include "macwin.h"
*/
import "C"

import (
	"html"
	"sync"
	"unsafe"

	"github.com/kKEo/memors/tray/internal/webpolicy"
)

// Windows implements app.Windows with native windows.
type Windows struct {
	mu      sync.Mutex
	origins map[string]string // open windows: key -> the origin they may show
}

var (
	registry   *Windows
	registryMu sync.Mutex
)

// Init prepares the app for windows. Call it once the menu-bar loop runs
// (systray's onReady); debug enables the web inspector.
func Init(debug bool) *Windows {
	registryMu.Lock()
	defer registryMu.Unlock()
	if registry == nil {
		registry = &Windows{origins: map[string]string{}}
		d := 0
		if debug {
			d = 1
		}
		C.macwin_init(C.int(d))
	}
	return registry
}

// Open shows url in the window for key, creating it or bringing it to the
// front. The window may then show only pages of origin.
func (w *Windows) Open(key, title, url, origin string) {
	w.mu.Lock()
	w.origins[key] = origin
	w.mu.Unlock()
	ck, ct, cu := C.CString(key), C.CString(title), C.CString(url)
	defer C.free(unsafe.Pointer(ck))
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cu))
	C.macwin_open(ck, ct, cu)
}

// ShowMessage replaces an open window's page with a note.
func (w *Windows) ShowMessage(key, text string) {
	if !w.Has(key) {
		return
	}
	page := `<!doctype html><meta charset="utf-8"><body style="font:15px -apple-system,sans-serif;color:#555;` +
		`display:grid;place-items:center;height:90vh;margin:0"><p>` + html.EscapeString(text) + `</p></body>`
	ck, ch := C.CString(key), C.CString(page)
	defer C.free(unsafe.Pointer(ck))
	defer C.free(unsafe.Pointer(ch))
	C.macwin_message(ck, ch)
}

// Has reports whether the window for key is open.
func (w *Windows) Has(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.origins[key]
	return ok
}

// decide is called on the main thread for every navigation in a window.
func (w *Windows) decide(key, target string, clicked bool) webpolicy.Decision {
	w.mu.Lock()
	origin, ok := w.origins[key]
	w.mu.Unlock()
	if !ok {
		return webpolicy.Deny
	}
	return webpolicy.Decide(origin, target, clicked)
}

//export macwinDecide
func macwinDecide(key, url *C.char, clicked C.int) C.int {
	registryMu.Lock()
	w := registry
	registryMu.Unlock()
	if w == nil {
		return C.int(webpolicy.Deny)
	}
	return C.int(w.decide(C.GoString(key), C.GoString(url), clicked != 0))
}

//export macwinClosed
func macwinClosed(key *C.char) {
	registryMu.Lock()
	w := registry
	registryMu.Unlock()
	if w == nil {
		return
	}
	w.mu.Lock()
	delete(w.origins, C.GoString(key))
	w.mu.Unlock()
}
