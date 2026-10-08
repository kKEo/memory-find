//go:build unix

package discover

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// trusted refuses a run directory that someone else could have written
// to. A missing directory is fine: no server has run yet.
func trusted(dir string) error {
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Getuid() || fi.Mode().Perm()&0o022 != 0 {
		return ErrUntrusted
	}
	return nil
}
