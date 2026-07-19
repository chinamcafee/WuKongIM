package wkdb

import (
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestAllowlistIdempotencyKeepsSingleSecondaryIndexPerMemberAndCount(t *testing.T) {
	wk := NewWukongDB(NewOptions(WithDir(t.TempDir()), WithShardNum(1))).(*wukongDB)
	require.NoError(t, wk.Open())
	t.Cleanup(func() { require.NoError(t, wk.Close()) })

	channelID := "allowlist-index-idempotent"
	channelType := uint8(1)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := wk.AddChannel(ChannelInfo{
		ChannelId:   channelID,
		ChannelType: channelType,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	})
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		writeTime := now.Add(time.Duration(i) * time.Minute)
		require.NoError(t, wk.AddAllowlist(channelID, channelType, []Member{
			{Uid: "uid-1", CreatedAt: &writeTime, UpdatedAt: &writeTime},
		}))
	}
	require.Equal(t, 1, countAllowlistSecondaryIndex(t, wk, channelID, channelType, key.TableAllowlist.SecondIndex.CreatedAt))
	require.Equal(t, 1, countAllowlistSecondaryIndex(t, wk, channelID, channelType, key.TableAllowlist.SecondIndex.UpdatedAt))
	require.Equal(t, 1, countChannelCountSecondaryIndex(t, wk, key.TableChannelInfo.SecondIndex.AllowlistCount))

	require.NoError(t, wk.AddAllowlist(channelID, channelType, []Member{{Uid: "uid-2", CreatedAt: &now, UpdatedAt: &now}}))
	require.Equal(t, 2, countAllowlistSecondaryIndex(t, wk, channelID, channelType, key.TableAllowlist.SecondIndex.CreatedAt))
	require.Equal(t, 1, countChannelCountSecondaryIndex(t, wk, key.TableChannelInfo.SecondIndex.AllowlistCount))

	require.NoError(t, wk.RemoveAllowlist(channelID, channelType, []string{"missing", "missing"}))
	require.NoError(t, wk.RemoveAllowlist(channelID, channelType, []string{"uid-2", "uid-2"}))
	require.Equal(t, 1, countAllowlistSecondaryIndex(t, wk, channelID, channelType, key.TableAllowlist.SecondIndex.CreatedAt))
	require.Equal(t, 1, countChannelCountSecondaryIndex(t, wk, key.TableChannelInfo.SecondIndex.AllowlistCount))

	require.NoError(t, wk.RemoveAllAllowlist(channelID, channelType))
	require.Equal(t, 0, countAllowlistSecondaryIndex(t, wk, channelID, channelType, key.TableAllowlist.SecondIndex.CreatedAt))
	require.Equal(t, 0, countAllowlistSecondaryIndex(t, wk, channelID, channelType, key.TableAllowlist.SecondIndex.UpdatedAt))
	require.Equal(t, 1, countChannelCountSecondaryIndex(t, wk, key.TableChannelInfo.SecondIndex.AllowlistCount))
}

func countAllowlistSecondaryIndex(
	t *testing.T,
	wk *wukongDB,
	channelID string,
	channelType uint8,
	indexName [2]byte,
) int {
	t.Helper()
	iter := wk.channelDb(channelID, channelType).NewIter(&pebble.IterOptions{
		LowerBound: key.NewAllowlistSecondIndexKey(channelID, channelType, indexName, 0, 0),
		UpperBound: key.NewAllowlistSecondIndexKey(channelID, channelType, indexName, math.MaxUint64, math.MaxUint64),
	})
	defer iter.Close()
	count := 0
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	require.NoError(t, iter.Error())
	return count
}

func countChannelCountSecondaryIndex(t *testing.T, wk *wukongDB, indexName [2]byte) int {
	t.Helper()
	iter := wk.dbs[0].NewIter(&pebble.IterOptions{
		LowerBound: key.NewChannelInfoSecondIndexKey(indexName, 0, 0),
		UpperBound: key.NewChannelInfoSecondIndexKey(indexName, math.MaxUint64, math.MaxUint64),
	})
	defer iter.Close()
	count := 0
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	require.NoError(t, iter.Error())
	return count
}
