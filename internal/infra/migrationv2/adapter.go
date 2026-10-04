package migrationv2

import "context"

// Reader implements the stopped-source port for the pinned original schema.
type Reader struct{}

func (Reader) ReadStoppedNode(ctx context.Context, opts NodeOptions, rows func(Row) error, files func(SourceFile) error) (NodeSnapshot, error) {
	return ReadStoppedNode(ctx, opts, rows, files)
}
