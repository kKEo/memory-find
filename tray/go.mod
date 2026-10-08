module github.com/kKEo/memory-find/tray

go 1.26.0

require github.com/kKEo/memory-find v0.0.0-00010101000000-000000000000

require (
	fyne.io/systray v1.12.2
	golang.org/x/sys v0.48.0
)

require github.com/godbus/dbus/v5 v5.1.0 // indirect

replace github.com/kKEo/memory-find => ../
