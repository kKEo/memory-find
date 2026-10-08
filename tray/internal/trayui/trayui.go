// Package trayui renders the menu model (internal/menu) in the menu bar
// with fyne.io/systray. When only titles change it updates the items in
// place, so an open menu does not flicker; any change of structure (a new
// agent, a server started) rebuilds it.
package trayui

import (
	"sync"

	"fyne.io/systray"

	"github.com/kKEo/memory-find/tray/internal/menu"
)

// UI is the menu bar. Render may be called from any goroutine except the
// main thread, which systray calls into.
type UI struct {
	act func(menu.Action)

	mu    sync.Mutex
	items []menu.Item
	nodes []*node
	title string
	built bool
}

type node struct {
	mi       *systray.MenuItem // nil for a separator
	children []*node
}

// New returns a UI that hands clicks to act, which must not block.
func New(act func(menu.Action)) *UI { return &UI{act: act} }

// Ready sets the icon; call it from systray's onReady.
func (u *UI) Ready(templateIcon []byte, tooltip string) {
	systray.SetTemplateIcon(templateIcon, templateIcon)
	systray.SetTooltip(tooltip)
}

// Render shows items and the menu-bar title.
func (u *UI) Render(items []menu.Item, title string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if title != u.title {
		systray.SetTitle(title)
		u.title = title
	}
	if u.built && menu.SameShape(u.items, items) {
		update(u.nodes, u.items, items)
	} else {
		systray.ResetMenu() // closes the click channels of the old items
		u.nodes = u.build(nil, items)
		u.built = true
	}
	u.items = items
}

func (u *UI) build(parent *systray.MenuItem, items []menu.Item) []*node {
	nodes := make([]*node, 0, len(items))
	for _, it := range items {
		if it.Separator {
			if parent == nil {
				systray.AddSeparator()
			} else {
				parent.AddSeparator()
			}
			nodes = append(nodes, &node{})
			continue
		}
		var mi *systray.MenuItem
		if parent == nil {
			mi = systray.AddMenuItem(it.Title, it.Tooltip)
		} else {
			mi = parent.AddSubMenuItem(it.Title, it.Tooltip)
		}
		if it.Disabled {
			mi.Disable()
		}
		n := &node{mi: mi, children: u.build(mi, it.Children)}
		if it.Action.Kind != menu.None && len(it.Children) == 0 {
			// systray drops a click when nobody is receiving, so every
			// clickable item has its own listener; it ends when the item
			// is removed (its channel is closed).
			go func(ch chan struct{}, act menu.Action) {
				for range ch {
					u.act(act)
				}
			}(mi.ClickedCh, it.Action)
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// update applies new titles, tooltips and enabled states to a menu of the
// same shape.
func update(nodes []*node, old, cur []menu.Item) {
	for i, it := range cur {
		n := nodes[i]
		if it.Separator || n.mi == nil {
			continue
		}
		if it.Title != old[i].Title {
			n.mi.SetTitle(it.Title)
		}
		if it.Tooltip != old[i].Tooltip {
			n.mi.SetTooltip(it.Tooltip)
		}
		if it.Disabled != old[i].Disabled {
			if it.Disabled {
				n.mi.Disable()
			} else {
				n.mi.Enable()
			}
		}
		update(n.children, old[i].Children, it.Children)
	}
}
