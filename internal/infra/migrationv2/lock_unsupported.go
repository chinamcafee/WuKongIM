//go:build !darwin && !linux

package migrationv2

import (
	"errors"
	"io"
)

func lockSource(string) (io.Closer, error) {
	return nil, errors.New("original v2 migration reader requires Linux or macOS source locking")
}
