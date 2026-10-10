// Package home finds memors-mcp's directories the way memors-mcp does:
// MEMORS_HOME, or ~/.memors-mcp.
package home

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Dir is MEMORS_HOME as an absolute path.
func Dir() (string, error) {
	if h := os.Getenv("MEMORS_HOME"); h != "" {
		return filepath.Abs(h)
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".memors-mcp"), nil
}

// KBDir holds the knowledge-base files.
func KBDir(home string) string { return filepath.Join(home, "kb") }

// LogDir holds the logs of servers memors-tray starts, and its own.
func LogDir(home string) string { return filepath.Join(home, "logs") }

// namePattern is memors-mcp's rule for MEMORS_KB (internal/kb.ValidateName).
var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidName reports whether name is a valid knowledge-base name.
func ValidName(name string) bool {
	return namePattern.MatchString(name) && name != "." && name != ".."
}

// KBs lists the knowledge bases on disk, sorted by name.
func KBs(home string) ([]string, error) {
	des, err := os.ReadDir(KBDir(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, de := range des {
		name, ok := strings.CutSuffix(de.Name(), ".db")
		if ok && de.Type().IsRegular() && ValidName(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}
