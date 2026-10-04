package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestPluginSettingsTopologyPreservesSelectedStateAndUnmappedConfigSource(t *testing.T) {
	for _, sourceCount := range []int{1, 3} {
		for _, targetCount := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%d_to_%d_node_cluster", sourceCount, targetCount), func(t *testing.T) {
				ctx := context.Background()
				p, c, w, no := pluginConfigFixture(t, 0)
				p.Sources = p.Sources[:sourceCount]
				c.Nodes = c.Nodes[:sourceCount]
				p.Target.Nodes = p.Target.Nodes[:targetCount]
				p.PluginNodes = nil
				chosen := uint64(1000 + sourceCount)
				for _, n := range p.Target.Nodes {
					p.PluginNodes = append(p.PluginNodes, migration.PluginNodeMapping{SourceNode: chosen, TargetNode: n.NodeID})
				}
				r := migrationv2.Reader{}
				report, err := migration.PreparePluginSettings(ctx, p, c, w, r)
				require.NoError(t, err)
				require.Equal(t, uint64(2*targetCount), report.Records)
				native := pluginStateInspector{states: map[uint64][]pluginhost.DesiredState{}}
				require.NoError(t, migration.WalkPluginSettings(ctx, w, *report, func(rec migration.MappedPluginSettings) error {
					require.Equal(t, chosen, rec.SourceNode)
					require.Contains(t, string(rec.Original.Config), fmt.Sprintf("secret-%d", sourceCount))
					require.Equal(t, sourceCount != 3, rec.Desired.Enabled)
					require.Equal(t, *rec.Original.CreatedAt, rec.Desired.CreatedAt)
					require.Equal(t, *rec.Original.UpdatedAt, rec.Desired.UpdatedAt)
					if rec.Desired.No == no {
						require.Contains(t, string(rec.Desired.Config), "secret-1")
						require.NotNil(t, rec.ConfigSource)
						require.Equal(t, uint64(1001), rec.ConfigSource.SourceNode)
					} else {
						require.Contains(t, string(rec.Desired.Config), fmt.Sprintf("secret-%d", sourceCount))
					}
					store := pluginhost.NewStore(filepath.Join(t.TempDir(), "plugin-state"))
					require.NoError(t, store.Save(rec.Desired))
					got, err := store.Load(rec.Desired.No)
					require.NoError(t, err)
					native.states[rec.TargetNode] = append(native.states[rec.TargetNode], got)
					return nil
				}))
				verified, err := migration.VerifyPluginSettings(ctx, p, c, ignoreAssignedSettingsWorkspace{w}, r, native)
				require.NoError(t, err)
				require.Equal(t, report.Records, verified.Records)
				repeated, err := migration.PreparePluginSettings(ctx, p, c, w, r)
				require.NoError(t, err)
				require.Equal(t, report, repeated)
				bad := c
				bad.Nodes = append([]migration.NodeSnapshot(nil), c.Nodes...)
				bad.Nodes[0].NodeID = 99
				_, err = migration.PreparePluginSettings(ctx, p, bad, w, r)
				require.Error(t, err)
				_, err = migration.VerifyPluginSettings(ctx, p, bad, w, r, native)
				require.Error(t, err)
			})
		}
	}
}

// Descriptor-only registrations allow full offline workflow coverage without
// treating a configuration test as acceptance of an active business plugin.
func TestPluginSettingsChangedTopologyArchiveAndNativeImport(t *testing.T) {
	testPluginSettingsAndArtifactsTopology(t, nil)
}

func testPluginSettingsAndArtifactsTopology(t *testing.T, program []byte) {
	t.Helper()
	var fixtures map[string][]struct{ Key, Value []byte }
	raw, err := os.ReadFile("testdata/original-v2-plugin-kv.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	for _, tc := range []struct{ sources, targets int }{{1, 3}, {3, 1}, {3, 2}, {3, 3}} {
		t.Run(fmt.Sprintf("%d_to_%d_node_cluster", tc.sources, tc.targets), func(t *testing.T) {
			ctx, r := context.Background(), migrationv2.Reader{}
			p := diagnosticPlan(t, "")
			p.Sources = nil
			p.Target.Nodes = nil
			var no string
			for n := 1; n <= tc.sources; n++ {
				name := "original-v2-server.tar.gz"
				if tc.sources > 1 {
					name = fmt.Sprintf("original-v2-three-%d.tar.gz", n)
				}
				dir := unpackNamedFixture(t, name)
				clearFixtureMessageExtensions(t, dir)
				db, err := pebble.Open(filepath.Join(dir, "db", "wukongimdb", "shard000"), &pebble.Options{ErrorIfNotExists: true})
				require.NoError(t, err)
				if len(program) > 0 {
					no = putProfilePluginFixture(t, db, n)
				} else {
					for _, kv := range fixtures["descriptor"] {
						value := kv.Value
						if kv.Key[2] == byte(migration.Primary) {
							switch binary.BigEndian.Uint16(kv.Key[len(kv.Key)-2:]) {
							case 0x1501:
								no = string(value)
							case 0x1506:
								value = binary.BigEndian.AppendUint32(nil, uint32(n-1))
							}
						}
						require.NoError(t, db.Set(kv.Key, value, pebble.Sync))
					}
				}
				require.NoError(t, db.Close())
				p.Sources = append(p.Sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
				if len(program) > 0 {
					path := filepath.Join(t.TempDir(), "source-plugin")
					require.NoError(t, os.WriteFile(path, program, 0700))
					p.PluginArtifacts = append(p.PluginArtifacts, migration.PluginArtifactSpec{SourceNode: uint64(n), PluginNo: no, Path: path, Bytes: int64(len(program)), SHA256: fmt.Sprintf("%x", sha256.Sum256(program)), Profile: migration.AIExampleReceiveProfile})
				}
			}
			for n := 1; n <= tc.targets; n++ {
				p.Target.Nodes = append(p.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 58200+n), DataDir: filepath.Join(t.TempDir(), "target")})
				p.PluginNodes = append(p.PluginNodes, migration.PluginNodeMapping{SourceNode: uint64(tc.sources), TargetNode: uint64(100 + n)})
			}
			p.Target.Replicas = uint16(tc.targets)
			p.Target.ChannelReplicas = uint16(tc.targets)
			p.PluginConfigs = []migration.PluginConfigMapping{{PluginNo: no, SourceNode: 1}}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
			require.NoError(t, err)
			require.Equal(t, "prepared", prepared.Status)
			require.Equal(t, uint64(tc.sources), prepared.Capture.Tables["Plugin"])
			require.Equal(t, uint64(tc.targets), prepared.PluginSettings.Records)
			if len(program) > 0 {
				require.Len(t, prepared.PluginArtifacts.Files, tc.sources)
				require.Len(t, prepared.PluginArtifacts.Targets, tc.targets)
				require.NotNil(t, prepared.PluginArtifacts.Compatibility)
			}
			archivePath := filepath.Join(t.TempDir(), "archive")
			archive, err := archivefs.NewFileArchiveStore(archivePath)
			require.NoError(t, err)
			_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: p.Digest(), SourceCommit: p.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
			require.NoError(t, err)
			for _, n := range p.Sources {
				require.NoError(t, os.Rename(n.DataDir, n.DataDir+"-unmounted"))
			}
			for _, f := range p.PluginArtifacts {
				require.NoError(t, os.Rename(f.Path, f.Path+"-unmounted"))
			}
			fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
			require.NoError(t, err)
			defer fresh.Close()
			rebuilt, err := migration.PrepareArchive(ctx, p, fresh, r, archive)
			require.NoError(t, err)
			require.Equal(t, prepared.PluginSettings, rebuilt.PluginSettings)
			require.Equal(t, prepared.PluginArtifacts, rebuilt.PluginArtifacts)
			options := migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings, PluginArtifacts: rebuilt.PluginArtifacts}
			if len(program) > 0 && tc.sources == 3 && tc.targets == 1 {
				broken := &failPluginCopy{Workspace: fresh}
				require.ErrorContains(t, migrationv3.Install(ctx, p.Target, rebuilt.Conversion, broken, options), "interrupted plugin copy")
				_, err := os.Stat(filepath.Join(p.Target.Nodes[0].DataDir, "MIGRATION-COMPLETE"))
				require.True(t, os.IsNotExist(err))
				// Model a killed writer after chmod but before rename. The
				// exact reserved partial belongs to this unfinished generation.
				require.NoError(t, os.WriteFile(filepath.Join(p.Target.Nodes[0].DataDir, "plugins", "."+no+".wkmigrate-partial"), []byte("partial"), 0500))
			}
			require.NoError(t, migrationv3.Install(ctx, p.Target, rebuilt.Conversion, fresh, options))
			require.NoError(t, migrationv3.Install(ctx, p.Target, rebuilt.Conversion, fresh, options))
			programVerified, err := migration.VerifyPluginArtifacts(ctx, p, ignoreAssignedSettingsWorkspace{fresh}, migrationv3.Inspector{})
			require.NoError(t, err)
			for _, node := range p.Target.Nodes {
				wantPrograms := uint64(0)
				if len(program) > 0 {
					wantPrograms = 1
				}
				require.Equal(t, wantPrograms, programVerified.ByTarget[node.NodeID])
			}
			if len(program) > 0 && tc.sources == 3 && tc.targets == 1 {
				verifyArtifactDrift(t, p, rebuilt, fresh, options, no)
			}
			if tc.sources == 1 && tc.targets == 3 {
				// Exercise composition after both original DB and executable
				// paths are unavailable; verification must use archive bytes.
				planPath := filepath.Join(t.TempDir(), "plan.json")
				data, err := json.Marshal(p)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(planPath, data, 0600))
				var out, diagnostics bytes.Buffer
				require.Equal(t, 0, migrationapp.Run(ctx, []string{"verify", "--plan", planPath, "--workspace", filepath.Join(t.TempDir(), "cli-workspace"), "--archive", archivePath}, &out, &diagnostics), diagnostics.String())
				var result migration.VerificationReport
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				require.Equal(t, "offline_verified", result.Status)
				require.NotNil(t, result.PluginArtifacts)
				require.Equal(t, programVerified.ByTarget, result.PluginArtifacts.ByTarget)
				require.False(t, result.CutoverReady)
			}
			for _, node := range p.Target.Nodes {
				state, err := pluginhost.NewStore(filepath.Join(node.DataDir, "plugin-state")).Load(no)
				require.NoError(t, err)
				require.Equal(t, tc.sources != 3, state.Enabled)
			}
			verified, err := migration.VerifyPluginSettings(ctx, p, rebuilt.Capture, ignoreAssignedSettingsWorkspace{fresh}, r, migrationv3.Inspector{})
			require.NoError(t, err)
			require.Equal(t, uint64(tc.targets), verified.Records)
			_, err = migration.VerifyTargets(ctx, p.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
			require.NoError(t, err)
		})
	}
}
