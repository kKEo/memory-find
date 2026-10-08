//go:build !darwin

// Command memo-tray is a macOS menu-bar companion for memo-mcp's HTTP
// servers. Other systems are not supported yet.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "memo-tray runs on macOS only")
	os.Exit(1)
}
