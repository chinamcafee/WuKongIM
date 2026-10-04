package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func putProfilePluginFixture(t *testing.T, db *pebble.DB, node int) string {
	t.Helper()
	no := "wk.plugin.ai-example"
	h := fnv.New64a()
	_, err := h.Write([]byte(no))
	require.NoError(t, err)
	fields := map[uint16][]byte{0x1501: []byte(no), 0x1502: []byte("AI example"), 0x1503: []byte(`{"name":{"type":"string"}}`), 0x1506: binary.BigEndian.AppendUint32(nil, uint32(node-1)), 0x1507: []byte("0.0.1"), 0x1508: []byte(`["Receive"]`), 0x1509: binary.BigEndian.AppendUint32(nil, 1), 0x150a: []byte(fmt.Sprintf(`{"name":"fixture-node-%d"}`, node))}
	for col, value := range fields {
		key := make([]byte, 14)
		binary.BigEndian.PutUint16(key, 0x1501)
		key[2] = byte(migration.Primary)
		binary.BigEndian.PutUint64(key[4:], h.Sum64())
		binary.BigEndian.PutUint16(key[12:], col)
		require.NoError(t, db.Set(key, value, pebble.Sync))
	}
	return no
}

func TestDescriptorOnlyRegistrationCannotAuthorizeUnknownExecutable(t *testing.T) {
	ctx, r := context.Background(), migrationv2.Reader{}
	dir := compatibleMessageFixture(t)
	db, err := pebble.Open(filepath.Join(dir, "db", "wukongimdb", "shard000"), &pebble.Options{ErrorIfNotExists: true})
	require.NoError(t, err)
	no := putProfilePluginFixture(t, db, 1)
	h := fnv.New64a()
	_, err = h.Write([]byte(no))
	require.NoError(t, err)
	for col, value := range map[uint16][]byte{0x1508: []byte(`[]`), 0x150a: []byte(`{}`)} {
		key := make([]byte, 14)
		binary.BigEndian.PutUint16(key, 0x1501)
		key[2] = byte(migration.Primary)
		binary.BigEndian.PutUint64(key[4:], h.Sum64())
		binary.BigEndian.PutUint16(key[12:], col)
		require.NoError(t, db.Set(key, value, pebble.Sync))
	}
	require.NoError(t, db.Close())
	p := diagnosticPlan(t, dir)
	p.PluginNodes = []migration.PluginNodeMapping{{SourceNode: p.Sources[0].NodeID, TargetNode: p.Target.Nodes[0].NodeID}}
	program := []byte("unknown executable with harmless-looking old registration")
	path := filepath.Join(t.TempDir(), "source-plugin")
	require.NoError(t, os.WriteFile(path, program, 0700))
	p.PluginArtifacts = []migration.PluginArtifactSpec{{SourceNode: p.Sources[0].NodeID, PluginNo: no, Path: path, Bytes: int64(len(program)), SHA256: fmt.Sprintf("%x", sha256.Sum256(program))}}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
	require.ErrorContains(t, err, "plugin executables require a verified business compatibility profile")
	require.Equal(t, uint64(0), prepared.Selection.PluginBusinessRows)
	require.Equal(t, uint64(1), prepared.Selection.PluginArtifactCompatibilityPending)
	require.True(t, prepared.Selection.ReplicaComparisonComplete)
	require.Empty(t, prepared.Selection.Digest)
	_, found, err := w.Get(ctx, []byte("workflow/PREPARED"))
	require.NoError(t, err)
	require.False(t, found)
}

// failPluginCopy interrupts the copy pass, after report validation has already
// read each captured executable once. Ordinary metadata reads are unaffected.
type failPluginCopy struct {
	migration.Workspace
	seen map[string]int
}

func (w *failPluginCopy) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	if strings.HasPrefix(string(prefix), "plugin-artifacts/") && strings.HasSuffix(string(prefix), "/chunks/") {
		if w.seen == nil {
			w.seen = map[string]int{}
		}
		w.seen[string(prefix)]++
		if w.seen[string(prefix)] == 2 {
			return w.Workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
				if err := visit(row); err != nil {
					return err
				}
				return errors.New("interrupted plugin copy")
			})
		}
	}
	return w.Workspace.Walk(ctx, prefix, visit)
}

func verifyArtifactDrift(t *testing.T, p migration.Plan, prepared migration.Preflight, w migration.Workspace, options migrationv3.InstallOptions, no string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(p.Target.Nodes[0].DataDir, "plugins", no+".wkp")
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, fault := range []string{"bytes", "mode", "missing", "symlink", "extra"} {
		t.Run("native-program-"+fault, func(t *testing.T) {
			switch fault {
			case "bytes":
				require.NoError(t, os.Chmod(path, 0600))
				changed := bytes.Clone(original)
				changed[0] ^= 1
				require.NoError(t, os.WriteFile(path, changed, 0500))
				require.NoError(t, os.Chmod(path, 0500))
			case "mode":
				require.NoError(t, os.Chmod(path, 0400))
			case "missing":
				require.NoError(t, os.Rename(path, path+"-hidden"))
			case "symlink":
				require.NoError(t, os.Rename(path, path+"-hidden"))
				require.NoError(t, os.Symlink(path+"-hidden", path))
			case "extra":
				require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "extra.wkp"), []byte("extra"), 0500))
			}
			_, err := migration.VerifyPluginArtifacts(ctx, p, w, migrationv3.Inspector{})
			require.Error(t, err)
			require.Error(t, migrationv3.Install(ctx, p.Target, prepared.Conversion, w, options))
			switch fault {
			case "bytes":
				require.NoError(t, os.Chmod(path, 0600))
				require.NoError(t, os.WriteFile(path, original, 0500))
				require.NoError(t, os.Chmod(path, 0500))
			case "mode":
				require.NoError(t, os.Chmod(path, 0500))
			case "missing":
				require.NoError(t, os.Rename(path+"-hidden", path))
			case "symlink":
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Rename(path+"-hidden", path))
			case "extra":
				require.NoError(t, os.Remove(filepath.Join(filepath.Dir(path), "extra.wkp")))
			}
		})
	}
	_, err = migration.VerifyPluginArtifacts(ctx, p, w, migrationv3.Inspector{})
	require.NoError(t, err)
}

func TestPluginArtifactCaptureRejectsChangedInputs(t *testing.T) {
	for _, fault := range []string{"valid", "hash", "short", "long", "mode", "symlink", "duplicate", "unsafe-name", "unknown-source"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			content := bytes.Repeat([]byte("original-plugin"), 100000)
			path := filepath.Join(t.TempDir(), "original")
			require.NoError(t, os.WriteFile(path, content, 0700))
			spec := migration.PluginArtifactSpec{SourceNode: 1, PluginNo: "original.plugin", Path: path, Bytes: int64(len(content)), SHA256: fmt.Sprintf("%x", sha256.Sum256(content))}
			p := migration.Plan{Sources: []migration.NodeOptions{{NodeID: 1}}, PluginNodes: []migration.PluginNodeMapping{{SourceNode: 1, TargetNode: 2}}, PluginArtifacts: []migration.PluginArtifactSpec{spec}}
			switch fault {
			case "hash":
				p.PluginArtifacts[0].SHA256 = strings.Repeat("0", 64)
			case "short":
				p.PluginArtifacts[0].Bytes++
			case "long":
				p.PluginArtifacts[0].Bytes--
			case "mode":
				require.NoError(t, os.Chmod(path, 0600))
			case "symlink":
				require.NoError(t, os.Rename(path, path+"-real"))
				require.NoError(t, os.Symlink(path+"-real", path))
			case "duplicate":
				p.PluginArtifacts = append(p.PluginArtifacts, spec)
			case "unsafe-name":
				p.PluginArtifacts[0].PluginNo = "../other"
			case "unknown-source":
				p.PluginArtifacts[0].SourceNode = 3
			}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			err = migration.CapturePluginArtifacts(ctx, p, w, migrationv2.Reader{})
			if fault != "valid" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NoError(t, migration.CapturePluginArtifacts(ctx, p, w, migrationv2.Reader{}))
			var output bytes.Buffer
			got, err := migration.WalkPluginArtifact(ctx, w, spec, func(chunk []byte) error {
				require.LessOrEqual(t, len(chunk), 1<<20)
				_, err := output.Write(chunk)
				return err
			})
			require.NoError(t, err)
			require.Equal(t, uint64(2), got.Chunks)
			require.Equal(t, content, output.Bytes())
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, content, actual)
			for _, change := range []string{"chunk-bytes", "missing-chunk", "extra-chunk", "descriptor"} {
				_, err := migration.WalkPluginArtifact(ctx, mutatedArtifactWorkspace{Workspace: w, fault: change}, spec, nil)
				require.Error(t, err, change)
			}
		})
	}
}

type mutatedArtifactWorkspace struct {
	migration.Workspace
	fault string
}

func (w mutatedArtifactWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	data, found, err := w.Workspace.Get(ctx, key)
	if err == nil && found && w.fault == "descriptor" && strings.HasSuffix(string(key), "/descriptor") {
		var descriptor migration.CapturedPluginArtifact
		if err := json.Unmarshal(data, &descriptor); err != nil {
			return nil, false, err
		}
		descriptor.Spec.SHA256 = strings.Repeat("0", 64)
		data, err = json.Marshal(descriptor)
	}
	return data, found, err
}

func (w mutatedArtifactWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	if !strings.HasPrefix(string(prefix), "plugin-artifacts/") || !strings.HasSuffix(string(prefix), "/chunks/") {
		return w.Workspace.Walk(ctx, prefix, visit)
	}
	first := true
	var last transfer.SpoolRow
	err := w.Workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		last = row
		if first {
			first = false
			if w.fault == "missing-chunk" {
				return nil
			}
			if w.fault == "chunk-bytes" {
				row.Value = bytes.Clone(row.Value)
				row.Value[0] ^= 1
			}
		}
		return visit(row)
	})
	if err == nil && w.fault == "extra-chunk" {
		last.Key = append(bytes.Clone(prefix), []byte("99999999")...)
		return visit(last)
	}
	return err
}
