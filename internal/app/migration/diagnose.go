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

// diagnose publishes details only after the census finishes. Failed runs keep
// a clearly incomplete file; they cannot replace a completed report or acquire
// the prepare workflow identity. Only the caller's scratch directory is used.
func diagnose(ctx context.Context, plan usecase.Plan, dir string, w usecase.Workspace, r migrationv2.Reader, stderr io.Writer) (any, error) {
	f, err := os.CreateTemp(dir, ".diagnostic-incomplete-*.jsonl")
	if err != nil {
		return nil, err
	}
	buffer := bufio.NewWriterSize(f, 64<<10)
	report, runErr := usecase.DiagnoseSources(ctx, plan, w, r, r, buffer, func(node uint64, stage string) { fmt.Fprintf(stderr, "source node %d: %s\n", node, stage) })
	err = errors.Join(runErr, buffer.Flush(), f.Sync(), f.Close())
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "diagnostic-findings-"+report.FindingsSHA256+".jsonl")
	if err := os.Rename(f.Name(), path); err != nil {
		return nil, err
	}
	report.FindingsFile = path
	return report, nil
}
