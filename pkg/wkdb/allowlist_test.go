package wkdb_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	allowlist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)
}

func TestGetAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	allowlist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	members, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(members))
}

func TestRemoveAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	allowlist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	err = d.RemoveAllowlist(channelId, channelType, []string{"uid1", "uid2"})
	assert.NoError(t, err)

	members, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(members))
}

func TestRemoveAllAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	allowlist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	err = d.RemoveAllAllowlist(channelId, channelType)
	assert.NoError(t, err)

	members, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(members))
}

func TestExistAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	allowlist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	exist, err := d.ExistAllowlist(channelId, channelType, "uid1")
	assert.NoError(t, err)
	assert.True(t, exist)
}

func TestHasAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	allowlist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	has, err := d.HasAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.True(t, has)
}

func TestAddAllowlistIsIdempotentForRepeatedAndMixedMembers(t *testing.T) {
	d := newTestDB(t)
	require.NoError(t, d.Open())
	t.Cleanup(func() { require.NoError(t, d.Close()) })

	channelID := "allowlist-idempotent"
	channelType := uint8(1)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := d.AddChannel(wkdb.ChannelInfo{
		ChannelId:   channelID,
		ChannelType: channelType,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	})
	require.NoError(t, err)

	firstCreatedAt := now.Add(-time.Hour)
	firstUpdatedAt := now.Add(-30 * time.Minute)
	require.NoError(t, d.AddAllowlist(channelID, channelType, []wkdb.Member{
		{Uid: "uid-1", CreatedAt: &firstCreatedAt, UpdatedAt: &firstUpdatedAt},
		{Uid: "uid-1", CreatedAt: &now, UpdatedAt: &now},
	}))

	for i := 0; i < 100; i++ {
		retryTime := now.Add(time.Duration(i+1) * time.Minute)
		require.NoError(t, d.AddAllowlist(channelID, channelType, []wkdb.Member{
			{Uid: "uid-1", CreatedAt: &retryTime, UpdatedAt: &retryTime},
		}))
	}

	require.NoError(t, d.AddAllowlist(channelID, channelType, []wkdb.Member{
		{Uid: "uid-1", CreatedAt: &now, UpdatedAt: &now},
		{Uid: "uid-2", CreatedAt: &now, UpdatedAt: &now},
		{Uid: "uid-2", CreatedAt: &now, UpdatedAt: &now},
		{Uid: "uid-3", CreatedAt: &now, UpdatedAt: &now},
	}))

	members, err := d.GetAllowlist(channelID, channelType)
	require.NoError(t, err)
	require.Len(t, members, 3)
	membersByUID := make(map[string]wkdb.Member, len(members))
	for _, member := range members {
		membersByUID[member.Uid] = member
	}
	require.True(t, membersByUID["uid-1"].CreatedAt.Equal(firstCreatedAt))
	require.True(t, membersByUID["uid-1"].UpdatedAt.Equal(firstUpdatedAt))

	channelInfo, err := d.GetChannel(channelID, channelType)
	require.NoError(t, err)
	require.Equal(t, 3, channelInfo.AllowlistCount)

	require.NoError(t, d.RemoveAllowlist(channelID, channelType, []string{"missing", "missing"}))
	require.NoError(t, d.RemoveAllowlist(channelID, channelType, []string{"uid-2", "uid-2"}))
	require.NoError(t, d.RemoveAllowlist(channelID, channelType, []string{"uid-2"}))
	channelInfo, err = d.GetChannel(channelID, channelType)
	require.NoError(t, err)
	require.Equal(t, 2, channelInfo.AllowlistCount)
}

func TestAddAllowlistConcurrentSameUID(t *testing.T) {
	d := newTestDB(t)
	require.NoError(t, d.Open())
	t.Cleanup(func() { require.NoError(t, d.Close()) })

	channelID := "allowlist-concurrent"
	channelType := uint8(1)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := d.AddChannel(wkdb.ChannelInfo{
		ChannelId:   channelID,
		ChannelType: channelType,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	})
	require.NoError(t, err)

	const writers = 32
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			writeTime := now.Add(time.Duration(index) * time.Second)
			errs <- d.AddAllowlist(channelID, channelType, []wkdb.Member{
				{Uid: "same-uid", CreatedAt: &writeTime, UpdatedAt: &writeTime},
			})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	members, err := d.GetAllowlist(channelID, channelType)
	require.NoError(t, err)
	require.Len(t, members, 1, fmt.Sprintf("members=%+v", members))
	channelInfo, err := d.GetChannel(channelID, channelType)
	require.NoError(t, err)
	require.Equal(t, 1, channelInfo.AllowlistCount)
}
