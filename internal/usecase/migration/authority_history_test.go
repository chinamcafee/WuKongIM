package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func historicalAuthorityFixture() *authorityFixture {
	f := newAuthorityFixture()
	f.config.Replicas, f.config.Learners = []uint64{1, 2, 3}, nil
	f.config.MigrateFrom, f.config.MigrateTo = 1, 1
	f.config.Version, f.config.SHA256, f.config.NonMigrationSHA256 = 1732364897806218890, "stored", "unchanged-fields"
	f.logs = map[uint64][]ChannelConfigLog{}
	for n := uint64(1); n <= 3; n++ {
		previous, applied := f.config, f.config
		previous.Version, previous.MigrateFrom, previous.MigrateTo = 9, 0, 0
		previous.SHA256 = "previous"
		applied.Version, applied.SHA256 = 10, "index-substituted"
		f.logs[n] = []ChannelConfigLog{
			{Index: 9, Term: 2, Config: previous, CommandSHA256: "previous-command", EncodedConfigSHA256: "previous-encoded"},
			{Index: 10, Term: 2, Config: applied, CommandSHA256: "last-command", EncodedVersion: f.config.Version, EncodedConfigSHA256: f.config.SHA256},
		}
	}
	return f
}

func TestHistoricalSelfLeaderMarkerNeedsCompleteRetainedProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*authorityFixture)
		pass bool
	}{
		{"exact_historical_transition", func(*authorityFixture) {}, true},
		{"no_predecessor", func(f *authorityFixture) { f.logs[2] = f.logs[2][1:] }, false},
		{"predecessor_changes_other_fields", func(f *authorityFixture) { f.logs[2][0].Config.NonMigrationSHA256 = "changed" }, false},
		{"previous_command_differs", func(f *authorityFixture) { f.logs[2][0].CommandSHA256 = "changed" }, false},
		{"last_command_differs", func(f *authorityFixture) { f.logs[2][1].CommandSHA256 = "changed" }, false},
		{"previous_unfinished_transition", func(f *authorityFixture) { f.logs[2][0].Config.MigrateTo = 3 }, false},
		{"previous_delete", func(f *authorityFixture) { f.logs[2][0].Deleted = true }, false},
		{"missing_stable_digest", func(f *authorityFixture) { f.config.NonMigrationSHA256 = "" }, false},
		{"wrong_encoded_version", func(f *authorityFixture) { f.logs[2][1].EncodedVersion++ }, false},
		{"wrong_encoded_fields", func(f *authorityFixture) { f.logs[2][1].EncodedConfigSHA256 = "changed" }, false},
		{"ordinary_index_version_marker", func(f *authorityFixture) { f.config.Version = 10; f.config.SHA256 = "index-substituted" }, false},
		{"follower_self_marker", func(f *authorityFixture) { f.config.MigrateFrom = 2; f.config.MigrateTo = 2 }, false},
		{"incomplete_membership", func(f *authorityFixture) { f.config.ReplicaMax = 4 }, false},
		{"history_conflict", func(f *authorityFixture) { f.messages[2][1].SHA256 = "different" }, false},
		{"formal_lag", func(f *authorityFixture) { f.messages[2] = f.messages[2][:1] }, false},
		{"configuration_conflict", func(f *authorityFixture) { f.conflictingConfig = 2 }, false},
		{"source_changed", func(f *authorityFixture) { f.changedNode = 2 }, false},
		{"pending_command", func(f *authorityFixture) { l := f.logs[2][1]; l.Index = 11; f.logs[2] = append(f.logs[2], l) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := historicalAuthorityFixture()
			tc.edit(f)
			plan := Plan{Sources: []NodeOptions{{NodeID: 1, Options: Options{ShardCount: 1}}, {NodeID: 2, Options: Options{ShardCount: 1}}, {NodeID: 3, Options: Options{ShardCount: 1}}}}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "history", 128<<20)
			require.NoError(t, err)
			defer w.Close()
			var out bytes.Buffer
			r, err := AuditSourceAuthority(context.Background(), plan, w, f, f, &out, nil)
			require.NoError(t, err)
			require.Equal(t, 3, r.Version)
			var channel AuthorityChannel
			for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
				var v AuthorityChannel
				require.NoError(t, json.Unmarshal(line, &v))
				if v.Type == "channel" {
					channel = v
				}
			}
			if tc.pass {
				require.Equal(t, "historical_self_leader_noop", channel.MigrationKind)
				require.Equal(t, "consistent_formal_replicas", channel.Class)
				require.Equal(t, uint64(1), channel.CandidateNode)
				require.Empty(t, channel.Reasons)
				for _, copy := range channel.ConfigCopies {
					require.Equal(t, f.config.Version, copy.Config.Version)
					require.Equal(t, "original_encoded_payload", copy.VersionRule)
					require.Equal(t, uint64(9), copy.PreviousApplied.Index)
				}
			} else {
				require.Zero(t, channel.CandidateNode)
				require.NotEmpty(t, channel.Reasons)
			}
			require.False(t, r.CutoverReady)
			_, prepared, err := w.Get(context.Background(), []byte("workflow/PREPARED"))
			require.NoError(t, err)
			require.False(t, prepared)
		})
	}
}
