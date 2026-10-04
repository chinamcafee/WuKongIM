package migration

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

// authority publishes a checksummed diagnostic, never a prepare/selection seal.
func authority(ctx context.Context, plan usecase.Plan, dir string, w usecase.Workspace, r migrationv2.Reader, stderr io.Writer) (any, error) {
	f, err := os.CreateTemp(dir, ".authority-incomplete-*.jsonl")
	if err != nil {
		return nil, err
	}
	buffer := bufio.NewWriterSize(f, 64<<10)
	report, runErr := usecase.AuditSourceAuthority(ctx, plan, w, r, r, buffer, func(node uint64, stage string) { fmt.Fprintf(stderr, "source node %d: %s\n", node, stage) })
	if err := errors.Join(runErr, buffer.Flush(), f.Sync(), f.Close()); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "authority-details-"+report.DetailsSHA256+".jsonl")
	if err := os.Rename(f.Name(), path); err != nil {
		return nil, err
	}
	report.DetailsFile = path
	return report, nil
}
