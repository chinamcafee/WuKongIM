//go:build !darwin && !linux

package migrationv3

import (
	"errors"
	"io"
)

func lockTarget(string) (io.Closer, error) {
	return nil, errors.New("offline migration verification requires Linux or macOS process-exclusion locks")
}

func lockImport(path string) (io.Closer, error) { return lockTarget(path) }
