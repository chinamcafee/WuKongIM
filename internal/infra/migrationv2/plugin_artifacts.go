package migrationv2

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

// OpenPluginArtifact reads a regular stopped-source executable without changing
// permissions or launching it. The use case verifies its complete planned hash.
func (Reader) OpenPluginArtifact(ctx context.Context, spec migration.PluginArtifactSpec) (io.ReadCloser, uint32, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	before, err := os.Lstat(spec.Path)
	if err != nil {
		return nil, 0, errors.New("cannot inspect source plugin file")
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Size() != spec.Bytes {
		return nil, 0, errors.New("source plugin must be a regular executable of the planned size")
	}
	f, err := os.Open(spec.Path)
	if err != nil {
		return nil, 0, errors.New("cannot open source plugin file")
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Size() != spec.Bytes || before.Mode() != after.Mode() {
		return nil, 0, errors.Join(errors.New("source plugin changed while opening"), f.Close())
	}
	return f, uint32(after.Mode().Perm()), nil
}
