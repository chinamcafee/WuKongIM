package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// Original SaveChannelClusterConfig/GetChannelClusterConfig and Raft ConfChange
// accept zero versions. A version is compared across the authoritative Slot
// replicas, not replaced with a fabricated positive generation.
func TestOriginalZeroConfigVersionRequiresReplicaAgreement(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflicting=%t", conflict), func(t *testing.T) {
			ctx, root := context.Background(), t.TempDir()
			var sources []migration.NodeOptions
			before := map[string]map[string][32]byte{}
			for i := 1; i <= 3; i++ {
				source := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", i))
				clearFixtureMessageExtensions(t, source)
				if !conflict || i == 1 {
					changed := rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
						if len(key) != 14 || binary.BigEndian.Uint16(key) != 0x0b01 || key[2] != 1 || binary.BigEndian.Uint16(key[12:]) != 0x0b0b {
							return false
						}
						require.NoError(t, b.Set(key, make([]byte, 8), nil))
						return true
					})
					require.Positive(t, changed)
				}
				sources = append(sources, migration.NodeOptions{NodeID: uint64(i), Options: migration.Options{DataDir: source, ShardCount: 2}})
				before[source] = fileDigests(t, source)
			}
			plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: sources, Target: migration.TargetPlan{ClusterID: "zero-source-version", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 3, ChannelReplicas: 3}}
			for i := 0; i < 3; i++ {
				plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: uint64(101 + i), Addr: fmt.Sprintf("127.0.0.1:%d", 57881+i), DataDir: filepath.Join(root, fmt.Sprintf("node%d", i))})
			}
			w, err := transfer.OpenSpool(filepath.Join(root, "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			r := migrationv2.Reader{}
			prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
			for _, source := range sources {
				require.Equal(t, before[source.DataDir], fileDigests(t, source.DataDir))
			}
			if conflict {
				require.ErrorContains(t, err, "conflicts between replica nodes")
				require.Empty(t, prepared.Status)
				_, found, err := w.Get(ctx, []byte("workflow/PREPARED"))
				require.NoError(t, err)
				require.False(t, found)
				for _, node := range plan.Target.Nodes {
					require.NoDirExists(t, node.DataDir)
				}
				return
			}
			require.NoError(t, err)
			var configs uint64
			require.NoError(t, migration.WalkSelectedSources(ctx, w, func(row migration.SelectedRecord) error {
				if row.Row.Table != "ChannelClusterConfig" {
					return nil
				}
				configs++
				require.Equal(t, make([]byte, 8), row.Row.Fields["ConfVersion"])
				d, err := r.Describe(row.Row, row.Identity)
				require.NoError(t, err)
				require.NotNil(t, d.Authority)
				require.Zero(t, d.Authority.Version)
				return nil
			}))
			require.Positive(t, configs)
			require.NoError(t, migrationv3.Install(ctx, plan.Target, prepared.Conversion, w))
			verified, err := migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, r, migrationv3.Inspector{})
			require.NoError(t, err)
			require.Equal(t, "offline_verified", verified.Status)
			require.Positive(t, prepared.Conversion.Messages)
			require.Equal(t, prepared.Conversion.Messages*3, verified.Messages)
		})
	}
}

func TestOriginalZeroConfigVersionStillRequiresCompleteAuthority(t *testing.T) {
	source := compatibleMessageFixture(t)
	var original migration.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(row migration.Row) error {
		if row.Table == "ChannelClusterConfig" && row.Kind == migration.Primary {
			original = row
		}
		return nil
	}))
	require.NotEmpty(t, original.Key)
	for _, tc := range []struct {
		name, field string
		value       []byte
	}{
		{"missing-version", "ConfVersion", nil},
		{"zero-term", "Term", make([]byte, 4)},
		{"zero-leader", "LeaderId", make([]byte, 8)},
		{"missing-replicas", "Replicas", nil},
		{"unknown-leader", "LeaderId", []byte{0, 0, 0, 0, 0, 0, 0, 99}},
		{"unfinished-election", "Status", []byte{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := original
			row.Fields = map[string][]byte{}
			for k, v := range original.Fields {
				row.Fields[k] = bytes.Clone(v)
			}
			row.Fields["ConfVersion"] = make([]byte, 8)
			row.Fields[tc.field] = tc.value
			id, err := migrationv2.Identify(row)
			require.NoError(t, err)
			_, err = (migrationv2.Reader{}).Describe(row, id)
			require.Error(t, err)
		})
	}
}

func TestOriginalZeroTypeSourceConfigurationKeepsItsControlIdentity(t *testing.T) {
	ctx := context.Background()
	source := compatibleMessageFixture(t)
	var template migration.Row
	require.NoError(t, migrationv2.Scan(ctx, migration.Options{DataDir: source, ShardCount: 2}, func(row migration.Row) error {
		if row.Table == "ChannelClusterConfig" && row.Kind == migration.Primary && template.Key == nil {
			template = row
		}
		return nil
	}))
	require.NotEmpty(t, template.Key)
	const channel = "source-type-zero"
	var columns []struct{ key, value []byte }
	// Copy the known original writer's complete columns, changing only identity
	// and its physical hash. No message/channel policy is fabricated for the row.
	db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", "shard000"), &pebble.Options{ErrorIfNotExists: true})
	require.NoError(t, err)
	iter := db.NewIter(&pebble.IterOptions{LowerBound: template.Key, UpperBound: append(bytes.Clone(template.Key), 0xff, 0xff)})
	for ok := iter.First(); ok; ok = iter.Next() {
		key, value := bytes.Clone(iter.Key()), bytes.Clone(iter.Value())
		binary.BigEndian.PutUint64(key[4:], counterHash(channel+"-0"))
		switch binary.BigEndian.Uint16(key[12:]) {
		case 0x0b01:
			value = []byte(channel)
		case 0x0b02:
			value = []byte{0}
		case 0x0b0b:
			value = make([]byte, 8)
		}
		columns = append(columns, struct{ key, value []byte }{key, value})
	}
	require.NoError(t, iter.Error())
	require.NoError(t, iter.Close())
	b := db.NewBatch()
	for _, column := range columns {
		require.NoError(t, b.Set(column.key, column.value, nil))
	}
	require.NoError(t, b.Commit(pebble.Sync))
	require.NoError(t, b.Close())
	require.NoError(t, db.Close())
	before := fileDigests(t, source)
	root := t.TempDir()
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "source-type-zero", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57881", DataDir: filepath.Join(root, "target")}}}}
	w, err := transfer.OpenSpool(filepath.Join(root, "spool"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	p, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	found := false
	require.NoError(t, migration.WalkSelectedSources(ctx, w, func(row migration.SelectedRecord) error {
		if row.Identity.Channel.ID != channel {
			return nil
		}
		found = true
		require.Equal(t, "ChannelClusterConfig", row.Row.Table)
		require.Zero(t, row.Identity.Channel.Type)
		require.Equal(t, []byte{0}, row.Row.Fields["ChannelType"])
		require.Equal(t, counterHash(channel+"-0"), row.Row.ID)
		return nil
	}))
	require.True(t, found)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, p.Conversion, w))
	verified, err := migration.VerifyTargets(ctx, plan.Target, p.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, "offline_verified", verified.Status)
	require.Equal(t, before, fileDigests(t, source))
}
