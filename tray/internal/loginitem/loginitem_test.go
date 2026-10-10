package loginitem

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestEnableDisable(t *testing.T) {
	a := Agent{Home: t.TempDir(), Executable: "/Applications/memo-tray & co.app/Contents/MacOS/memo-tray"}
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
	for _, want := range []string{"<string>io.github.kkeo.memo-tray</string>", "memo-tray &amp; co.app", "<key>RunAtLoad</key>\n\t<true/>"} {
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

	moved := Agent{Home: a.Home, Executable: "/Users/me/Applications/memo-tray.app/Contents/MacOS/memo-tray"}
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
	a := Agent{Home: t.TempDir(), Executable: "/private/var/folders/x/AppTranslocation/ABC/d/memo-tray.app/Contents/MacOS/memo-tray"}
	if err := a.Enable(); !errors.Is(err, ErrTranslocated) || a.Enabled() {
		t.Errorf("translocated: %v", err)
	}
	if err := (Agent{Home: t.TempDir(), Executable: "memo-tray"}).Enable(); err == nil {
		t.Error("relative path accepted")
	}
}
