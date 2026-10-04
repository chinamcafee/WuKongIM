package migrationv2

import (
	"errors"
	"io"
	"os"

	"github.com/cockroachdb/pebble/vfs"
)

// sourceFS rejects all filesystem mutations, including Pebble's ordinary
// truncate/create lock operation. Its lock implementation only opens an
// existing file and takes a shared fcntl lock excluding original v2 writers.
type sourceFS struct{}

var errReadOnly = errors.New("migration source is read-only")

func (sourceFS) Open(n string, o ...vfs.OpenOption) (vfs.File, error) {
	return vfs.Default.Open(n, o...)
}
func (sourceFS) OpenDir(n string) (vfs.File, error)             { return vfs.Default.OpenDir(n) }
func (sourceFS) List(n string) ([]string, error)                { return vfs.Default.List(n) }
func (sourceFS) Stat(n string) (os.FileInfo, error)             { return vfs.Default.Stat(n) }
func (sourceFS) PathBase(n string) string                       { return vfs.Default.PathBase(n) }
func (sourceFS) PathDir(n string) string                        { return vfs.Default.PathDir(n) }
func (sourceFS) PathJoin(n ...string) string                    { return vfs.Default.PathJoin(n...) }
func (sourceFS) GetDiskUsage(n string) (vfs.DiskUsage, error)   { return vfs.Default.GetDiskUsage(n) }
func (sourceFS) Lock(n string) (io.Closer, error)               { return lockSource(n) }
func (sourceFS) Create(string) (vfs.File, error)                { return nil, errReadOnly }
func (sourceFS) ReuseForWrite(string, string) (vfs.File, error) { return nil, errReadOnly }
func (sourceFS) Link(string, string) error                      { return errReadOnly }
func (sourceFS) Remove(string) error                            { return errReadOnly }
func (sourceFS) RemoveAll(string) error                         { return errReadOnly }
func (sourceFS) Rename(string, string) error                    { return errReadOnly }
func (sourceFS) MkdirAll(string, os.FileMode) error             { return errReadOnly }
