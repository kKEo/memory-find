//go:build !darwin

package discover

import (
	"os"
	"syscall"
)

// Inspect reports only whether pid exists (memors-tray targets macOS).
func Inspect(pid int) Process {
	p, err := os.FindProcess(pid)
	if err != nil || pid <= 0 {
		return Process{}
	}
	return Process{Alive: p.Signal(syscall.Signal(0)) == nil}
}
