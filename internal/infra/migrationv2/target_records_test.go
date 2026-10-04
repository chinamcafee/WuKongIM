package migrationv2_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestSyntheticCompatibleSourceProducesNativeBusinessRecords(t *testing.T) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "native-records", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleMessageFixture(t), ShardCount: 2}}}, r, w, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, w, r)
	require.NoError(t, err)
	selected, err := migration.SelectSources(ctx, capture, catalog, w, r, nil)
	require.NoError(t, err)
	report, err := migration.BuildTargetRecords(ctx, selected, w, r)
	require.NoError(t, err)
	require.Equal(t, uint64(4), report.Metadata["channel"], "ordinary, allowlist, denylist and system registry must be explicit native rows")
	require.Equal(t, uint64(4), report.Messages)
	require.Equal(t, uint64(2), report.MessageChannels)
	require.Equal(t, uint64(2096462782110109696), report.MaxMessageID)
	var ordinary meta.UserChannelMembership
	var cmd meta.UserCMDChannelMembership
	var group meta.Channel
	var device meta.Device
	var allow, deny, system int
	require.NoError(t, migration.WalkTargetMetadata(ctx, w, func(record migration.TargetRecord) error {
		switch record.Table {
		case "channel":
			var value meta.Channel
			require.NoError(t, migration.UnmarshalState(record.Value, &value))
			if value.ChannelID == "migrationgroup" {
				group = value
			}
		case "membership":
			var value meta.UserChannelMembership
			require.NoError(t, migration.UnmarshalState(record.Value, &value))
			if value.UID == "migrationbob" {
				ordinary = value
			}
		case "cmd_membership":
			var value meta.UserCMDChannelMembership
			require.NoError(t, migration.UnmarshalState(record.Value, &value))
			if value.UID == "migrationalice" {
				cmd = value
			}
		case "device":
			var value meta.Device
			require.NoError(t, migration.UnmarshalState(record.Value, &value))
			if value.UID == "migrationbob" {
				device = value
			}
		case "subscriber":
			var value meta.Subscriber
			require.NoError(t, migration.UnmarshalState(record.Value, &value))
			switch value.ChannelID {
			case "__wk_internal_memberlist__/allow/2/bWlncmF0aW9uZ3JvdXA":
				allow++
			case "__wk_internal_memberlist__/deny/2/bWlncmF0aW9uZ3JvdXA":
				deny++
			case "__wk_internal_system_uids__":
				system++
			}
		}
		return nil
	}))
	require.Equal(t, uint64(2), group.SubscriberCount, "source stored count is stale zero")
	require.Equal(t, uint64(3), ordinary.ReadSeq)
	require.Equal(t, uint64(3), ordinary.DeletedToSeq)
	require.Equal(t, uint64(1), ordinary.JoinSeq)
	require.False(t, ordinary.Tombstone)
	require.Equal(t, uint64(1), cmd.StartSeq)
	require.Equal(t, uint64(1), cmd.AckSeq)
	require.Equal(t, int64(1), device.DeviceFlag)
	require.Equal(t, int64(1), device.DeviceLevel)
	require.Equal(t, 2, allow)
	require.Equal(t, 1, deny)
	require.Equal(t, 1, system)
	resumed, err := migration.BuildTargetRecords(ctx, selected, w, r)
	require.NoError(t, err)
	require.Equal(t, report, resumed)
}

// The original empty group is invisible despite having a stored conversation timestamp.
func TestOriginalEmptyGroupPreservesMembershipWithoutInventingAConversation(t *testing.T) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "empty-original", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: unpackNamedFixture(t, "original-v2-empty.tar.gz"), ShardCount: 2}}}, r, w, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, w, r)
	require.NoError(t, err)
	selected, err := migration.SelectSources(ctx, capture, catalog, w, r, nil)
	require.NoError(t, err)
	report, err := migration.BuildTargetRecords(ctx, selected, w, r)
	require.NoError(t, err)
	require.Equal(t, uint64(1), report.Metadata["membership"])
	require.NoError(t, migration.WalkTargetMetadata(ctx, w, func(row migration.TargetRecord) error {
		if row.Table != "membership" {
			return nil
		}
		var member meta.UserChannelMembership
		require.NoError(t, migration.UnmarshalState(row.Value, &member))
		require.Equal(t, "emptyalice", member.UID)
		require.Equal(t, "emptygroup", member.ChannelID)
		require.Equal(t, uint64(1), member.JoinSeq)
		require.Zero(t, member.ActivatedAt)
		require.Zero(t, member.ReadSeq)
		return nil
	}))
}
