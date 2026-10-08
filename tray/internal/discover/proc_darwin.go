package discover

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// Inspect asks the kernel about pid: whether it exists, its command name
// and when it started.
func Inspect(pid int) Process {
	if pid <= 0 {
		return Process{}
	}
	if err := unix.Kill(pid, 0); err != nil && !errors.Is(err, unix.EPERM) {
		return Process{}
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(k.Proc.P_pid) != pid {
		return Process{Alive: true}
	}
	return Process{
		Alive:   true,
		Name:    unix.ByteSliceToString(k.Proc.P_comm[:]),
		Started: time.Unix(k.Proc.P_starttime.Sec, int64(k.Proc.P_starttime.Usec)*int64(time.Microsecond)),
	}
}
