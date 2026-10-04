package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestPrepareRejectsOriginalGlobalPluginBusinessWithoutUserBindings(t *testing.T) {
	var fixtures map[string][]struct{ Key, Value []byte }
	data, err := os.ReadFile("testdata/original-v2-plugin-kv.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.Len(t, fixtures, 5)
	for name, rows := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := compatibleMessageFixture(t)
			// Replay exact raw columns produced and read through original v2 public
			// AddOrUpdatePlugin/GetPlugin, only into this private fixture copy.
			db, err := pebble.Open(filepath.Join(dir, "db", "wukongimdb", "shard000"), &pebble.Options{})
			require.NoError(t, err)
			batch := db.NewBatch()
			for _, row := range rows {
				require.NoError(t, batch.Set(row.Key, row.Value, nil))
			}
			require.NoError(t, batch.Commit(pebble.Sync))
			require.NoError(t, batch.Close())
			require.NoError(t, db.Close())
			before := fileDigests(t, dir)
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "plugin-check", 128<<20)
			require.NoError(t, err)
			defer w.Close()
			reader := migrationv2.Reader{}
			plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "plugin-check", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 1, Addr: "127.0.0.1:57882", DataDir: filepath.Join(t.TempDir(), "node")}}}}
			plan.PluginNodes = []migration.PluginNodeMapping{{SourceNode: 1, TargetNode: 1}}
			// Explicit config choice must not bypass an active-plugin compatibility gate.
			plan.PluginConfigs = []migration.PluginConfigMapping{{PluginNo: string(originalPluginSettingsRow(t).Fields["No"]), SourceNode: 1}}
			// Each original fixture has a distinct plugin identity.
			for _, kv := range rows {
				if kv.Key[2] == byte(migration.Primary) && kv.Key[len(kv.Key)-2] == 0x15 && kv.Key[len(kv.Key)-1] == 0x01 {
					plan.PluginConfigs[0].PluginNo = string(kv.Value)
				}
			}
			if name == "global_send" {
				program := []byte("archived bytes are not business compatibility evidence")
				path := filepath.Join(t.TempDir(), "plugin")
				require.NoError(t, os.WriteFile(path, program, 0700))
				plan.PluginArtifacts = []migration.PluginArtifactSpec{{SourceNode: 1, PluginNo: plan.PluginConfigs[0].PluginNo, Path: path, Bytes: int64(len(program)), SHA256: fmt.Sprintf("%x", sha256.Sum256(program))}}
			}
			result, err := migration.Prepare(context.Background(), plan, w, reader, reader, nil)
			if name == "global_send" {
				require.NotNil(t, result.PluginArtifacts)
			}
			require.NotNil(t, result.PluginSettings)
			require.Equal(t, uint64(1), result.PluginSettings.Records)
			if name == "descriptor" {
				require.NoError(t, err)
				require.Equal(t, "prepared", result.Status)
				require.Positive(t, result.Selection.Preserved["old_management_data"])
			} else {
				require.ErrorContains(t, err, "plugin business methods/config")
				require.Empty(t, result.Status)
				require.True(t, result.Selection.ReplicaComparisonComplete)
				require.Equal(t, uint64(1), result.Selection.PluginBusinessRows)
				require.Positive(t, result.Selection.Tables["Message"])
				require.Empty(t, result.Selection.Digest)
				require.Empty(t, result.Conversion.Digest)
				_, prepared, checkpointErr := w.Get(context.Background(), []byte("workflow/PREPARED"))
				require.NoError(t, checkpointErr)
				require.False(t, prepared)
				_, conversionErr := migration.BuildTargetRecords(context.Background(), result.Selection, w, reader)
				require.ErrorContains(t, conversionErr, "complete selected source")
				archive, archiveErr := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "blocked-archive"))
				require.NoError(t, archiveErr)
				_, archiveErr = migration.ExportSourceArchive(context.Background(), migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, result.Capture, result.Catalog, result.Selection, w, archive)
				require.Error(t, archiveErr)
				// A later independent error must remain visible alongside the
				// plugin gate, rather than certifying partial replica comparison.
				partial, selectionErr := migration.SelectSources(context.Background(), result.Capture, result.Catalog, w, failingMessageDescription{reader}, nil)
				require.ErrorContains(t, selectionErr, "independent message description failure")
				require.ErrorContains(t, selectionErr, "plugin business methods/config")
				require.False(t, partial.ReplicaComparisonComplete)
				require.Empty(t, partial.Digest)
				require.NotContains(t, err.Error(), "synthetic-do-not-log")
			}
			require.Equal(t, before, fileDigests(t, dir))
			if name == "descriptor" {
				archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
				require.NoError(t, err)
				_, err = migration.ExportSourceArchive(context.Background(), migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, result.Capture, result.Catalog, result.Selection, w, archive)
				require.NoError(t, err)
				require.NoError(t, os.Rename(dir, dir+"-unmounted"))
				fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "plugin-settings-archive", 128<<20)
				require.NoError(t, err)
				defer fresh.Close()
				rebuilt, err := migration.PrepareArchive(context.Background(), plan, fresh, reader, archive)
				require.NoError(t, err)
				require.Equal(t, result.PluginSettings, rebuilt.PluginSettings)
				require.NoError(t, migrationv3.Install(context.Background(), plan.Target, rebuilt.Conversion, fresh, migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings}))
				require.NoError(t, migrationv3.Install(context.Background(), plan.Target, rebuilt.Conversion, fresh, migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings}))
				verified, err := migration.VerifyPluginSettings(context.Background(), plan, rebuilt.Capture, fresh, reader, migrationv3.Inspector{})
				require.NoError(t, err)
				require.Equal(t, uint64(1), verified.Records)
				_, err = migration.VerifyTargets(context.Background(), plan.Target, rebuilt.Selection, fresh, reader, migrationv3.Inspector{})
				require.NoError(t, err)
				dirState := filepath.Join(plan.Target.Nodes[0].DataDir, "plugin-state")
				no := plan.PluginConfigs[0].PluginNo
				statePath := filepath.Join(dirState, no+".json")
				originalBytes, err := os.ReadFile(statePath)
				require.NoError(t, err)
				store := pluginhost.NewStore(dirState)
				for _, fault := range []string{"config", "enabled", "timestamp", "missing", "extra", "symlink", "corrupt"} {
					t.Run("native-settings-"+fault, func(t *testing.T) {
						original, err := store.Load(no)
						require.NoError(t, err)
						switch fault {
						case "config":
							original.Config = []byte(`{"secret":"incorrect"}`)
							require.NoError(t, store.Save(original))
						case "enabled":
							original.Enabled = !original.Enabled
							require.NoError(t, store.Save(original))
						case "timestamp":
							original.UpdatedAt = original.UpdatedAt.Add(time.Nanosecond)
							require.NoError(t, store.Save(original))
						case "missing":
							require.NoError(t, os.Remove(statePath))
						case "extra":
							extra := original
							extra.No = "unexpected"
							require.NoError(t, store.Save(extra))
						case "symlink":
							require.NoError(t, os.Remove(statePath))
							require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "absent"), statePath))
						case "corrupt":
							require.NoError(t, os.WriteFile(statePath, []byte(`{"secret":`), 0600))
						}
						_, err = migration.VerifyPluginSettings(context.Background(), plan, rebuilt.Capture, fresh, reader, migrationv3.Inspector{})
						require.Error(t, err)
						require.Contains(t, err.Error(), "plugin settings")
						require.NotContains(t, err.Error(), "secret")
						require.Error(t, migrationv3.Install(context.Background(), plan.Target, rebuilt.Conversion, fresh, migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings}))
						if fault == "extra" {
							require.NoError(t, store.Delete("unexpected"))
						}
						if fault == "symlink" {
							require.NoError(t, os.Remove(statePath))
						}
						require.NoError(t, os.WriteFile(statePath, originalBytes, 0600))
					})
				}
				require.NoError(t, migrationv3.Install(context.Background(), plan.Target, rebuilt.Conversion, fresh, migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings}))
				// A generation with config policy cannot be resumed using a policy-free identity.
				require.Error(t, migrationv3.Install(context.Background(), plan.Target, rebuilt.Conversion, fresh))
				// Simulate interruption before READY publication. The importer must check
				// existing desired state itself, without relying on a sealed file digest.
				require.NoError(t, os.Remove(filepath.Join(plan.Target.Nodes[0].DataDir, "MIGRATION-READY")))
				require.NoError(t, os.Remove(filepath.Join(plan.Target.Nodes[0].DataDir, "MIGRATION-COMPLETE")))
				changed, err := store.Load(no)
				require.NoError(t, err)
				changed.Enabled = !changed.Enabled
				require.NoError(t, store.Save(changed))
				require.ErrorContains(t, migrationv3.Install(context.Background(), plan.Target, rebuilt.Conversion, fresh, migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings}), "existing plugin settings differ")
				retained, err := store.Load(no)
				require.NoError(t, err)
				require.Equal(t, changed.Enabled, retained.Enabled)
				_, err = os.Stat(filepath.Join(plan.Target.Nodes[0].DataDir, "MIGRATION-COMPLETE"))
				require.True(t, os.IsNotExist(err))
				require.NoError(t, os.WriteFile(statePath, originalBytes, 0600))
				require.NoError(t, migrationv3.Install(context.Background(), plan.Target, rebuilt.Conversion, fresh, migrationv3.InstallOptions{PluginSettings: rebuilt.PluginSettings}))
				_, err = migration.VerifyPluginSettings(context.Background(), plan, rebuilt.Capture, fresh, reader, migrationv3.Inspector{})
				require.NoError(t, err)
				_, err = migration.VerifyTargets(context.Background(), plan.Target, rebuilt.Selection, fresh, reader, migrationv3.Inspector{})
				require.NoError(t, err)

			}
			if name == "global_send" {
				planPath := filepath.Join(t.TempDir(), "plan.json")
				data, err := json.Marshal(plan)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(planPath, data, 0600))
				var output, diagnostics bytes.Buffer
				code := migrationapp.Run(context.Background(), []string{"prepare", "--plan", planPath, "--workspace", filepath.Join(t.TempDir(), "cli-spool")}, &output, &diagnostics)
				require.Equal(t, 1, code)
				require.Contains(t, diagnostics.String(), "plugin business methods/config")
				var failed migration.Preflight
				require.NoError(t, json.Unmarshal(output.Bytes(), &failed))
				require.Equal(t, "blocked", failed.Status)
				require.True(t, failed.Selection.ReplicaComparisonComplete)
				require.False(t, failed.CutoverReady)
				require.Empty(t, failed.Selection.Digest)
				_, err = os.Stat(plan.Target.Nodes[0].DataDir)
				require.True(t, os.IsNotExist(err))
				diagnostic, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "plugin-diagnostic", 128<<20)
				require.NoError(t, err)
				defer diagnostic.Close()
				var details bytes.Buffer
				report, err := migration.DiagnoseSources(context.Background(), plan, diagnostic, reader, reader, &details, nil)
				require.NoError(t, err)
				require.Equal(t, "blocked", report.Status)
				require.False(t, report.CutoverReady)
				found := false
				for _, category := range report.Categories {
					if category.Code == "plugin.business_mapping" {
						require.Len(t, category.Samples, 1)
						proof := category.Samples[0].Plugin
						require.NotNil(t, proof)
						require.Len(t, proof.FieldsSHA256, 64)
						require.Len(t, proof.ConfigJSONSHA256, 64)
						require.Positive(t, proof.MethodCount)
						found = true
					}
				}
				require.True(t, found)
				require.NotContains(t, details.String(), "synthetic-do-not-log")
				_, sealed, err := diagnostic.Get(context.Background(), []byte("workflow/PREPARED"))
				require.NoError(t, err)
				require.False(t, sealed)
				// Omitting legacy Stream storage cannot authorize omitting a hook.
				plan.Exclusions = &migration.Exclusions{LegacyStreamStorage: true}
				other, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "plugin-exclusion-check", 128<<20)
				require.NoError(t, err)
				defer other.Close()
				result, err := migration.Prepare(context.Background(), plan, other, reader, reader, nil)
				require.ErrorContains(t, err, "plugin business methods/config")
				require.Empty(t, result.Status)
			}
		})
	}
}

type failingMessageDescription struct{ migrationv2.Reader }

func (r failingMessageDescription) Describe(row migration.Row, id migration.RecordIdentity) (migration.RecordDescription, error) {
	if row.Table == "Message" {
		return migration.RecordDescription{}, errors.New("independent message description failure")
	}
	return r.Reader.Describe(row, id)
}

func TestPluginDiagnosticConfigDigestPreservesDifferencesWithoutLeakingValues(t *testing.T) {
	describe := func(config string) migration.PluginDiagnosticEvidence {
		d, err := (migrationv2.Reader{}).Describe(migration.Row{Table: "Plugin", Kind: migration.Primary, Fields: map[string][]byte{
			"No": []byte("private-plugin"), "Methods": []byte(`["Receive"]`), "Config": []byte(config),
		}}, migration.RecordIdentity{})
		require.NoError(t, err)
		data, err := json.Marshal(d.Plugin.Evidence)
		require.NoError(t, err)
		require.NotContains(t, string(data), "private-plugin")
		require.NotContains(t, string(data), "synthetic-secret")
		return d.Plugin.Evidence
	}
	a := describe(`{"name":"synthetic-secret","nested":{"b":2,"a":9007199254740992}}`)
	b := describe(`{ "nested": { "a":9007199254740992, "b":2 }, "name":"synthetic-secret" }`)
	c := describe(`{"name":"synthetic-secret","nested":{"b":2,"a":9007199254740993}}`)
	d := describe(`{"name":"different","nested":{"b":2,"a":9007199254740992}}`)
	require.Equal(t, a.ConfigJSONSHA256, b.ConfigJSONSHA256)
	require.NotEqual(t, a.FieldsSHA256, b.FieldsSHA256, "raw provenance must retain formatting")
	require.NotEqual(t, a.ConfigJSONSHA256, c.ConfigJSONSHA256, "large integers cannot collapse through float64")
	require.NotEqual(t, a.ConfigJSONSHA256, d.ConfigJSONSHA256)
	require.Equal(t, 2, a.ConfigFieldCount)
	require.Equal(t, 1, a.MethodCount)
}

func TestOriginalPluginDescriptionRejectsMalformedBusinessFieldsWithoutLoggingContent(t *testing.T) {
	for _, field := range []string{"Methods", "Config"} {
		t.Run(field, func(t *testing.T) {
			_, err := (migrationv2.Reader{}).Describe(migration.Row{Table: "Plugin", Kind: migration.Primary, Fields: map[string][]byte{"No": []byte("legacy"), field: []byte(`"synthetic-private-value"`)}}, migration.RecordIdentity{})
			require.ErrorContains(t, err, "invalid original plugin")
			require.NotContains(t, err.Error(), "synthetic-private-value")
		})
	}
}
