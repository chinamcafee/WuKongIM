//go:build darwin || linux

package migrationv3

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func lockTarget(path string) (io.Closer, error) {
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

func lockImport(path string) (io.Closer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	spec := unix.Flock_t{Type: unix.F_WRLCK, Whence: io.SeekStart, Len: 0}
	if err := unix.FcntlFlock(f.Fd(), unix.F_SETLK, &spec); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
