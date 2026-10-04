package migrationv2_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// streamExclusionFixture marks only a private synthetic copy. Seq 1 exercises
// the Setting bit without StreamNo; seq 3 exercises StreamNo without the bit.
func streamExclusionFixture(t *testing.T, all bool) string {
	t.Helper()
	source := compatibleMessageFixture(t)
	rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) != 22 || binary.BigEndian.Uint16(key) != 0x0101 || key[2] != 1 {
			return false
		}
		seq := binary.BigEndian.Uint64(key[12:20])
		col := binary.BigEndian.Uint16(key[20:])
		if col == 0x0102 && (seq == 1 || all) {
			require.NoError(t, b.Set(key, []byte{2}, nil))
			return true
		}
		if col == 0x010e && seq == 3 {
			require.NoError(t, b.Set(key, []byte("synthetic-old-stream"), nil))
			return true
		}
		return false
	})
	return source
}

func TestStreamExclusionNativeConversionVerificationAndArchive(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, nodes := range []int{1, 3} {
			t.Run(fmt.Sprintf("all_%t_to_%d_node_cluster", all, nodes), func(t *testing.T) {
				ctx := context.Background()
				source := streamExclusionFixture(t, all)
				before := fileDigests(t, source)
				p := diagnosticPlan(t, source)
				p.Messages = &migration.MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, ExcludeStreams: true, CompactSequences: true}
				p.Target.Replicas = uint16(nodes)
				p.Target.ChannelReplicas = uint16(nodes)
				for i := 2; i <= nodes; i++ {
					p.Target.Nodes = append(p.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + i), Addr: fmt.Sprintf("127.0.0.1:%d", 57880+i), DataDir: filepath.Join(t.TempDir(), "target")})
				}
				w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "scratch"), p.Digest(), 128<<20)
				require.NoError(t, err)
				defer w.Close()
				r := migrationv2.Reader{}
				prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
				require.NoError(t, err)
				want := uint64(1)
				if all {
					want = 0
				}
				require.Equal(t, want, prepared.Conversion.Messages)
				require.EqualValues(t, 3-want, prepared.Conversion.Transformation.StreamDrops)
				require.EqualValues(t, 1, prepared.Conversion.Transformation.CMDDrops)
				require.NoError(t, migration.WalkTargetMetadata(ctx, w, func(row migration.TargetRecord) error {
					if row.Table == "membership" {
						var m meta.UserChannelMembership
						require.NoError(t, migration.UnmarshalState(row.Value, &m))
						if m.UID == "migrationbob" {
							require.Equal(t, want, m.ReadSeq)
							require.Equal(t, want, m.DeletedToSeq)
						}
					}
					return nil
				}))
				require.NoError(t, migrationv3.Install(ctx, p.Target, prepared.Conversion, w))
				verified, err := migration.VerifyTargets(ctx, p.Target, prepared.Selection, w, r, migrationv3.Inspector{})
				require.NoError(t, err)
				require.Equal(t, want*uint64(nodes), verified.Messages)
				require.Equal(t, prepared.Conversion.Transformation, verified.Transformation)
				archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
				require.NoError(t, err)
				_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: p.Digest(), SourceCommit: p.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
				require.NoError(t, err)
				fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "rebuild"), p.Digest(), 128<<20)
				require.NoError(t, err)
				defer fresh.Close()
				rebuilt, err := migration.PrepareArchive(ctx, p, fresh, r, archive)
				require.NoError(t, err)
				require.Equal(t, prepared.Conversion, rebuilt.Conversion)
				_, err = migration.VerifyTargets(ctx, p.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
				require.NoError(t, err)
				require.Equal(t, before, fileDigests(t, source))
			})
		}
	}
}
