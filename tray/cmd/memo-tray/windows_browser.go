//go:build darwin

package main

// browserWindows opens server pages in the default browser; it stands in
// for native windows.
type browserWindows struct{}

func newWindows() browserWindows { return browserWindows{} }

func (browserWindows) Open(_, _, url, _ string)   { _ = openURL(url) }
func (browserWindows) ShowMessage(string, string) {}
func (browserWindows) Has(string) bool            { return false }
