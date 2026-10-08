// Package locate finds the memo-mcp executable. An app started from Finder
// gets a minimal PATH, so the usual install directories are searched too.
package locate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound means no memo-mcp executable was found.
var ErrNotFound = errors.New("memo-mcp not found: install it, or set memo_binary in tray.json")

// MemoBinary returns the memo-mcp to run: the configured path, else one
// next to memo-tray (inside the app bundle), on PATH, or in a usual install
// directory.
func MemoBinary(configured string) (string, error) {
	userHome, _ := os.UserHomeDir()
	if configured != "" {
		p := expand(configured, userHome)
		if !executable(p) {
			return "", errors.New("memo_binary in tray.json is not an executable: " + configured)
		}
		return p, nil
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "memo-mcp"))
	}
	if p, err := exec.LookPath("memo-mcp"); err == nil {
		candidates = append(candidates, p)
	}
	for _, dir := range []string{"~/.local/bin", "/opt/homebrew/bin", "/usr/local/bin", "~/go/bin"} {
		candidates = append(candidates, filepath.Join(expand(dir, userHome), "memo-mcp"))
	}
	for _, p := range candidates {
		if executable(p) {
			if abs, err := filepath.Abs(p); err == nil {
				return abs, nil
			}
			return p, nil
		}
	}
	return "", ErrNotFound
}

// Version runs `memo-mcp version` and returns the version it reports.
func Version(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(string(out), "\n")
	if v, ok := strings.CutPrefix(strings.TrimSpace(first), "memo-mcp "); ok && v != "" {
		return v, nil
	}
	return "", errors.New("unexpected `memo-mcp version` output")
}

func expand(p, userHome string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok && userHome != "" {
		return filepath.Join(userHome, rest)
	}
	return p
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
