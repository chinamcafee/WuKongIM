//go:build darwin || linux

package migrationv2

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func lockSource(path string) (io.Closer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	spec := unix.Flock_t{Type: unix.F_RDLCK, Whence: io.SeekStart, Len: 0}
	if err := unix.FcntlFlock(f.Fd(), unix.F_SETLK, &spec); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
