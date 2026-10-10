// Package loginitem starts memors-tray when you log in, with a per-user
// LaunchAgent (~/Library/LaunchAgents/io.github.kkeo.memors-tray.plist).
// The agent takes effect at the next login; memors-tray never loads it into
// the running session, so turning it on does not start a second copy.
package loginitem

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Label identifies the LaunchAgent.
const Label = "io.github.kkeo.memors-tray"

// LegacyLabel is the LaunchAgent memors-tray had when it was memo-tray.
const LegacyLabel = "io.github.kkeo.memo-tray"

// ErrTranslocated means macOS is running memors-tray from a temporary copy
// (App Translocation), a path that will not exist at the next login.
var ErrTranslocated = errors.New("macOS is running memors-tray from a temporary location; move memors-tray.app to Applications and open it from there")

// Agent manages the LaunchAgent for one user.
type Agent struct {
	Home       string // the user's home directory
	Executable string // memors-tray's executable, as it should start at login
}

// Path is the agent's plist.
func (a Agent) Path() string {
	return filepath.Join(a.Home, "Library", "LaunchAgents", Label+".plist")
}

// Enabled reports whether memors-tray starts at login.
func (a Agent) Enabled() bool {
	_, err := os.Stat(a.Path())
	return err == nil
}

var plist = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Program}}</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
</dict>
</plist>
`))

// Enable writes the LaunchAgent.
func (a Agent) Enable() error {
	if strings.Contains(a.Executable, "/AppTranslocation/") {
		return ErrTranslocated
	}
	if !filepath.IsAbs(a.Executable) {
		return fmt.Errorf("not an absolute path: %s", a.Executable)
	}
	var esc bytes.Buffer
	if err := xml.EscapeText(&esc, []byte(a.Executable)); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := plist.Execute(&b, struct{ Label, Program string }{Label, esc.String()}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.Path()), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(a.Path()), ".memors-tray-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), a.Path())
}

// Disable removes the LaunchAgent; it is not an error if there is none.
func (a Agent) Disable() error {
	if err := os.Remove(a.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Program is the executable the agent starts, or "" when it is off.
func (a Agent) Program() string {
	b, err := os.ReadFile(a.Path())
	if err != nil {
		return ""
	}
	var doc struct {
		Dict struct {
			Items []struct {
				XMLName xml.Name
				Value   string   `xml:",chardata"`
				Strings []string `xml:"string"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return ""
	}
	items := doc.Dict.Items
	for i := 0; i+1 < len(items); i++ {
		if items[i].XMLName.Local == "key" && items[i].Value == "ProgramArguments" && len(items[i+1].Strings) > 0 {
			return items[i+1].Strings[0]
		}
	}
	return ""
}

// AdoptLegacy carries "open at login" over from memo-tray: when its
// LaunchAgent exists, this agent is enabled in its place and the old one is
// removed. A start from a temporary location (App Translocation) leaves the
// old agent for a later start to adopt.
func (a Agent) AdoptLegacy() error {
	old := filepath.Join(a.Home, "Library", "LaunchAgents", LegacyLabel+".plist")
	if _, err := os.Stat(old); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if strings.Contains(a.Executable, "/AppTranslocation/") {
		return nil
	}
	if err := a.Enable(); err != nil {
		return err
	}
	return os.Remove(old)
}

// Repair points an enabled agent at Executable again, after memors-tray
// was moved; it does nothing when the agent is off or already right.
func (a Agent) Repair() error {
	if !a.Enabled() || a.Program() == a.Executable || strings.Contains(a.Executable, "/AppTranslocation/") {
		return nil
	}
	return a.Enable()
}
