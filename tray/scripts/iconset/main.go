// Command iconset writes memo-tray's app icon as a macOS .iconset
// directory for iconutil (scripts/build-app.sh runs it).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kKEo/memory-find/tray/internal/icon"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: iconset <dir>.iconset")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, pt := range []int{16, 32, 128, 256, 512} {
		for _, scale := range []int{1, 2} {
			name := fmt.Sprintf("icon_%dx%d.png", pt, pt)
			if scale == 2 {
				name = fmt.Sprintf("icon_%dx%d@2x.png", pt, pt)
			}
			if err := os.WriteFile(filepath.Join(dir, name), icon.App(pt*scale), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
}
