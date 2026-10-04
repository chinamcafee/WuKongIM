package migrationv2_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

type originalPluginBindingFixture struct {
	UID         string `json:"uid"`
	PluginNo    string `json:"plugin_no"`
	CreatedAtNS int64  `json:"created_at_ns"`
	Rows        []struct{ Key, Value []byte }
}

func readOriginalPluginBinding(t *testing.T) (originalPluginBindingFixture, migration.SourcePluginBinding) {
	t.Helper()
	var f originalPluginBindingFixture
	data, err := os.ReadFile("testdata/original-v2-plugin-bindings.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &f))
	return f, migration.SourcePluginBinding{SourceID: counterHash(f.PluginNo + "_" + f.UID), UID: f.UID, PluginNo: f.PluginNo, CreatedAtNS: f.CreatedAtNS, UpdatedAtNS: f.CreatedAtNS}
}

// addSyntheticPluginBindings reuses the original public-API fixture's exact
// columns and two indexes. Additional identities are synthetic derivatives,
// not new evidence about original runtime behavior.
func addSyntheticPluginBindings(t *testing.T, dir string, bindings []migration.SourcePluginBinding) {
	t.Helper()
	f, _ := readOriginalPluginBinding(t)
	db, err := pebble.Open(filepath.Join(dir, "db", "wukongimdb", "shard000"), &pebble.Options{ErrorIfNotExists: true})
	require.NoError(t, err)
	b := db.NewBatch()
	for _, p := range bindings {
		id := counterHash(p.PluginNo + "_" + p.UID)
		for _, row := range f.Rows {
			key, value := append([]byte(nil), row.Key...), append([]byte(nil), row.Value...)
			if key[2] == byte(migration.Primary) {
				binary.BigEndian.PutUint64(key[4:12], id)
				switch binary.BigEndian.Uint16(key[12:]) {
				case 0x1601:
					value = []byte(p.PluginNo)
				case 0x1602:
					value = []byte(p.UID)
				case 0x1603:
					value = binary.BigEndian.AppendUint64(nil, uint64(p.CreatedAtNS))
				case 0x1604:
					value = binary.BigEndian.AppendUint64(nil, uint64(p.UpdatedAtNS))
				}
			} else {
				owner := p.UID
				if binary.BigEndian.Uint16(key[4:6]) == 0x1602 {
					owner = p.PluginNo
				}
				binary.BigEndian.PutUint64(key[6:14], counterHash(owner))
				binary.BigEndian.PutUint64(key[14:22], id)
			}
			require.NoError(t, b.Set(key, value, nil))
		}
	}
	require.NoError(t, b.Commit(pebble.Sync))
	require.NoError(t, b.Close())
	require.NoError(t, db.Close())
}

func preparePluginBindingFixture(t *testing.T, sourceCount, targetCount, replicas int, bindings []migration.SourcePluginBinding) (migration.Plan, migration.Preflight, *transfer.Spool) {
	t.Helper()
	var sources []migration.NodeOptions
	for n := 1; n <= sourceCount; n++ {
		name := "original-v2-server.tar.gz"
		if sourceCount > 1 {
			name = fmt.Sprintf("original-v2-three-%d.tar.gz", n)
		}
		dir := unpackNamedFixture(t, name)
		clearFixtureMessageExtensions(t, dir)
		addSyntheticPluginBindings(t, dir, bindings)
		sources = append(sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
	}
	plan := diagnosticPlan(t, sources[0].DataDir)
	plan.Sources = sources
	plan.Target.Replicas, plan.Target.ChannelReplicas = uint16(replicas), uint16(replicas)
	for n := 2; n <= targetCount; n++ {
		plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 57930+n), DataDir: filepath.Join(t.TempDir(), "target")})
	}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	r := migrationv2.Reader{}
	p, err := migration.Prepare(context.Background(), plan, w, r, r, nil)
	require.NoError(t, err)
	return plan, p, w
}

func TestPluginBindingArchiveImportRedistributesNativeReplicas(t *testing.T) {
	_, original := readOriginalPluginBinding(t)
	negative := migration.SourcePluginBinding{UID: "another-user", PluginNo: "second-plugin", CreatedAtNS: -1000001, UpdatedAtNS: -1}
	for _, tc := range []struct{ sources, targets, replicas int }{{1, 1, 1}, {1, 3, 1}, {3, 1, 1}, {3, 3, 3}} {
		t.Run(fmt.Sprintf("%d_to_%d_node_cluster_%d_replicas", tc.sources, tc.targets, tc.replicas), func(t *testing.T) {
			ctx, r := context.Background(), migrationv2.Reader{}
			plan, p, w := preparePluginBindingFixture(t, tc.sources, tc.targets, tc.replicas, []migration.SourcePluginBinding{original, negative})
			require.Equal(t, uint64(2), p.Conversion.Metadata["plugin_binding"])
			archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
			require.NoError(t, err)
			_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, p.Capture, p.Catalog, p.Selection, w, archive)
			require.NoError(t, err)
			for _, source := range plan.Sources {
				require.NoError(t, os.Rename(source.DataDir, source.DataDir+"-unmounted"))
			}
			fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer fresh.Close()
			rebuilt, err := migration.PrepareArchive(ctx, plan, fresh, r, archive)
			require.NoError(t, err)
			require.Equal(t, p.Selection, rebuilt.Selection)
			require.Equal(t, p.Conversion, rebuilt.Conversion)
			require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh))
			require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh), "exact retry")
			verified, err := migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
			require.NoError(t, err)
			require.Equal(t, uint64(2*tc.replicas), verified.Metadata["plugin_binding"])
			require.False(t, verified.CutoverReady)
			for _, node := range plan.Target.Nodes {
				db, err := meta.Open(filepath.Join(node.DataDir, "slotmeta"))
				require.NoError(t, err)
				for _, source := range []migration.SourcePluginBinding{original, negative} {
					s := db.MetaDB().HashSlot(uint16(crc32.ChecksumIEEE([]byte(source.UID)) % 256))
					rows, err := s.ListPluginBindingsByUID(ctx, source.UID)
					require.NoError(t, err)
					if len(rows) > 0 {
						require.Equal(t, []meta.PluginUserBinding{{UID: source.UID, PluginNo: source.PluginNo, CreatedAtMS: time.Unix(0, source.CreatedAtNS).UnixMilli(), UpdatedAtMS: time.Unix(0, source.UpdatedAtNS).UnixMilli()}}, rows)
					}
				}
				require.NoError(t, db.Close())
			}
		})
	}
}

func TestPluginBindingReverseIndexPagesAcrossNativeStringLengths(t *testing.T) {
	_, p := readOriginalPluginBinding(t)
	var bindings []migration.SourcePluginBinding
	// Native strings sort by length first. Long a-prefixed UIDs must follow
	// short z-prefixed UIDs even across the 128-row adapter page boundary.
	for _, format := range []string{"z%05d", "a%010d"} {
		count := 0
		for n := 0; count < 130; n++ {
			uid := fmt.Sprintf(format, n)
			if crc32.ChecksumIEEE([]byte(uid))%256 != 7 {
				continue
			}
			copy := p
			copy.UID = uid
			bindings = append(bindings, copy)
			count++
		}
	}
	plan, prepared, w := preparePluginBindingFixture(t, 1, 1, 1, bindings)
	ctx := context.Background()
	require.NoError(t, migrationv3.Install(ctx, plan.Target, prepared.Conversion, w))
	verified, err := migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, migrationv2.Reader{}, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, uint64(260), verified.Metadata["plugin_binding"])
	for _, mode := range []string{"missing", "duplicate", "timestamp", "identity"} {
		t.Run(mode, func(t *testing.T) {
			_, err := migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, migrationv2.Reader{}, faultyPluginIndexInspector{mode})
			require.ErrorContains(t, err, "plugin binding reverse index")
		})
	}
	for _, mode := range []string{"primary-missing", "primary-timestamp"} {
		t.Run(mode, func(t *testing.T) {
			_, err := migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, migrationv2.Reader{}, faultyPluginIndexInspector{mode})
			require.ErrorContains(t, err, "plugin_binding row")
		})
	}
}

type faultyPluginIndexInspector struct{ mode string }

func (i faultyPluginIndexInspector) Open(ctx context.Context, p migration.TargetPlan, n migration.TargetNode) (migration.TargetView, error) {
	v, err := (migrationv3.Inspector{}).Open(ctx, p, n)
	if err != nil {
		return nil, err
	}
	return faultyPluginIndexView{v, i.mode}, nil
}

type faultyPluginIndexView struct {
	migration.TargetView
	mode string
}

func (v faultyPluginIndexView) Metadata(ctx context.Context, table, owner string, key map[string]any) (map[string]any, bool, error) {
	row, found, err := v.TargetView.Metadata(ctx, table, owner, key)
	if err != nil || !found || table != "plugin_binding" {
		return row, found, err
	}
	switch v.mode {
	case "primary-missing":
		return nil, false, nil
	case "primary-timestamp":
		row["updated_at_ms"] = int64(0)
	}
	return row, found, nil
}

func (v faultyPluginIndexView) WalkPluginBindings(ctx context.Context, slot uint16, no string, visit func(meta.PluginUserBinding) error) error {
	first := true
	return v.TargetView.WalkPluginBindings(ctx, slot, no, func(p meta.PluginUserBinding) error {
		if first {
			first = false
			switch v.mode {
			case "missing":
				return nil
			case "duplicate":
				if err := visit(p); err != nil {
					return err
				}
			case "timestamp":
				p.UpdatedAtMS++
			case "identity":
				p.PluginNo = "different-plugin"
			}
		}
		return visit(p)
	})
}

func TestPluginBindingIndependentVerificationRejectsAlteredStorage(t *testing.T) {
	_, original := readOriginalPluginBinding(t)
	plan, p, w := preparePluginBindingFixture(t, 1, 3, 1, []migration.SourcePluginBinding{original})
	ctx := context.Background()
	for _, mode := range []string{"missing", "timestamp", "plugin", "uid", "extra", "outside-owner"} {
		t.Run(mode, func(t *testing.T) {
			for n := range plan.Target.Nodes {
				plan.Target.Nodes[n].DataDir = filepath.Join(t.TempDir(), "target")
			}
			require.NoError(t, migrationv3.Install(ctx, plan.Target, p.Conversion, w))
			slot := uint16(crc32.ChecksumIEEE([]byte(original.UID)) % 256)
			changed := false
			for _, node := range plan.Target.Nodes {
				view, err := (migrationv3.Inspector{}).Open(ctx, plan.Target, node)
				require.NoError(t, err)
				owned, err := view.OwnsMetadata(original.UID)
				require.NoError(t, err)
				require.NoError(t, view.Close())
				if (mode == "outside-owner" && owned) || (mode != "outside-owner" && !owned) {
					continue
				}
				db, err := meta.Open(filepath.Join(node.DataDir, "slotmeta"))
				require.NoError(t, err)
				s := db.MetaDB().HashSlot(slot)
				binding, found, err := s.GetPluginUserBinding(ctx, original.UID, original.PluginNo)
				require.NoError(t, err)
				if !changed && ((mode == "outside-owner" && !found) || (mode != "outside-owner" && found)) {
					changed = true
					if mode == "outside-owner" {
						binding = meta.PluginUserBinding{UID: original.UID, PluginNo: original.PluginNo, CreatedAtMS: time.Unix(0, original.CreatedAtNS).UnixMilli(), UpdatedAtMS: time.Unix(0, original.UpdatedAtNS).UnixMilli()}
					} else if mode != "extra" {
						require.NoError(t, s.UnbindPluginUser(ctx, binding.UID, binding.PluginNo))
					}
					switch mode {
					case "timestamp":
						binding.UpdatedAtMS++
					case "plugin", "extra":
						binding.PluginNo = "wrong-plugin"
					case "uid":
						binding.UID = "wrong-user"
					}
					if mode != "missing" {
						require.NoError(t, s.BindPluginUser(ctx, binding))
					}
				}
				require.NoError(t, db.Close())
				break
			}
			require.True(t, changed)
			_, err := migration.VerifyTargets(ctx, plan.Target, p.Selection, w, migrationv2.Reader{}, migrationv3.Inspector{})
			require.Error(t, err)
			require.Contains(t, err.Error(), "plugin")
			require.NotContains(t, err.Error(), original.UID)
		})
	}
}
