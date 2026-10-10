package loginitem

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnableDisable(t *testing.T) {
	a := Agent{Home: t.TempDir(), Executable: "/Applications/memors-tray & co.app/Contents/MacOS/memors-tray"}
	if a.Enabled() || a.Program() != "" {
		t.Fatal("enabled before Enable")
	}
	if err := a.Enable(); err != nil {
		t.Fatal(err)
	}
	if !a.Enabled() || a.Program() != a.Executable {
		t.Fatalf("after Enable: enabled=%v program=%q", a.Enabled(), a.Program())
	}
	b, _ := os.ReadFile(a.Path())
	for _, want := range []string{"<string>io.github.kkeo.memors-tray</string>", "memors-tray &amp; co.app", "<key>RunAtLoad</key>\n\t<true/>"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plist lacks %q:\n%s", want, b)
		}
	}
	if fi, _ := os.Stat(a.Path()); fi.Mode().Perm() != 0o644 {
		t.Errorf("plist mode %o", fi.Mode().Perm())
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("plutil", "-lint", a.Path()).CombinedOutput(); err != nil {
			t.Errorf("plutil: %v %s", err, out)
		}
	}

	moved := Agent{Home: a.Home, Executable: "/Users/me/Applications/memors-tray.app/Contents/MacOS/memors-tray"}
	if err := moved.Repair(); err != nil || moved.Program() != moved.Executable {
		t.Errorf("Repair: %v, program %q", err, moved.Program())
	}
	if err := a.Disable(); err != nil || a.Enabled() {
		t.Errorf("Disable: %v", err)
	}
	if err := a.Disable(); err != nil {
		t.Errorf("Disable twice: %v", err)
	}
	if err := moved.Repair(); err != nil || moved.Enabled() {
		t.Error("Repair turned a disabled agent on")
	}
}

func TestEnableRefusesTemporaryLocations(t *testing.T) {
	a := Agent{Home: t.TempDir(), Executable: "/private/var/folders/x/AppTranslocation/ABC/d/memors-tray.app/Contents/MacOS/memors-tray"}
	if err := a.Enable(); !errors.Is(err, ErrTranslocated) || a.Enabled() {
		t.Errorf("translocated: %v", err)
	}
	if err := (Agent{Home: t.TempDir(), Executable: "memors-tray"}).Enable(); err == nil {
		t.Error("relative path accepted")
	}
}

// memo-tray's LaunchAgent gives way to memors-tray's, keeping "open at login" on.
func TestAdoptLegacy(t *testing.T) {
	a := Agent{Home: t.TempDir(), Executable: "/Applications/memors-tray.app/Contents/MacOS/memors-tray"}
	if err := a.AdoptLegacy(); err != nil || a.Enabled() {
		t.Fatalf("without a legacy agent: %v, enabled=%v", err, a.Enabled())
	}
	old := filepath.Join(a.Home, "Library", "LaunchAgents", LegacyLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	translocated := Agent{Home: a.Home, Executable: "/private/var/folders/x/AppTranslocation/ABC/d/memors-tray.app/Contents/MacOS/memors-tray"}
	if err := translocated.AdoptLegacy(); err != nil || a.Enabled() {
		t.Fatalf("translocated: %v, enabled=%v", err, a.Enabled())
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("a translocated start removed the old agent: %v", err)
	}

	if err := a.AdoptLegacy(); err != nil {
		t.Fatal(err)
	}
	if !a.Enabled() || a.Program() != a.Executable {
		t.Errorf("after adopting: enabled=%v program=%q", a.Enabled(), a.Program())
	}
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("old agent still there: %v", err)
	}
}
