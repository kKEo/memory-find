//go:build !darwin

// Command memors-tray is a macOS menu-bar companion for memors-mcp's HTTP
// servers. Other systems are not supported yet.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "memors-tray runs on macOS only")
	os.Exit(1)
}
