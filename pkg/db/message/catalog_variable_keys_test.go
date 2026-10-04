package message

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Catalog keys are length-prefixed on disk; raw string order is not the cursor
// order. Page boundaries must preserve longer keys that sort lower as strings.
func TestCatalogPaginationPreservesVariableLengthKeys(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	ctx := context.Background()
	expected := []ChannelKey{"z", "aa", "aaa", "zaa"}
	for index, key := range expected {
		log := mustAcquireChannel(t, store.db, key, ChannelID{ID: string(key), Type: 2})
		_, err := log.Append(ctx, testRecords(uint64(index+100), "seed"), AppendOptions{})
		require.NoError(t, err)
	}
	for _, limit := range []int{1, 2, 3} {
		var seen []ChannelKey
		after := ChannelKey("")
		for pageIndex := 0; pageIndex <= len(expected); pageIndex++ {
			page, next, more, err := store.db.ListChannelsPage(ctx, after, limit)
			require.NoError(t, err)
			for _, entry := range page {
				seen = append(seen, entry.Key)
			}
			if !more {
				break
			}
			require.NotEqual(t, after, next)
			after = next
		}
		require.Equal(t, expected, seen)
		seen = nil
		req := InspectMessageRequest{Limit: limit}
		for pageIndex := 0; pageIndex <= len(expected); pageIndex++ {
			page, err := InspectChannels(ctx, store.db, req)
			require.NoError(t, err)
			for _, row := range page.Rows {
				seen = append(seen, ChannelKey(row["channel_key"].(string)))
			}
			if page.Done {
				break
			}
			require.NotNil(t, page.Next)
			req.AfterChannelKey = page.Next.AfterChannelKey
		}
		require.Equal(t, expected, seen)
	}
	page, err := InspectChannels(ctx, store.db, InspectMessageRequest{ChannelKey: "aa", AfterChannelKey: "z", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1, "point queries use the same encoded catalog cursor order")
}
