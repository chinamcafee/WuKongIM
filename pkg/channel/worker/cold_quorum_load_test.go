package worker

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

type coldQuorumStore struct {
	*store.MemoryChannelStore
}

func (s *coldQuorumStore) Load(context.Context) (store.InitialState, error) {
	return store.InitialState{}, nil
}
func (s *coldQuorumStore) LoadExactState(context.Context) (store.ExactState, error) {
	return store.ExactState{InitialState: store.InitialState{LEO: 17, HW: 12, CheckpointHW: 12}}, nil
}

type coldQuorumFactory struct{ value store.ChannelStore }

func (f coldQuorumFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	return f.value, nil
}

func TestQuorumColdLoadUsesConsistentDurableFrontier(t *testing.T) {
	cs := &coldQuorumStore{MemoryChannelStore: &store.MemoryChannelStore{}}
	result := ownershipStoreLoadTask().Run(context.Background(), Deps{Stores: coldQuorumFactory{value: cs}, QuorumLog: &captureDurableQuorumLog{}})
	require.NoError(t, result.Err)
	require.Equal(t, store.InitialState{LEO: 17, HW: 12, CheckpointHW: 12}, result.StoreLoad.Initial)
	require.NoError(t, result.StoreLoad.Store.Close())
}

func TestQuorumColdLoadClosesStoreWithoutExactCapability(t *testing.T) {
	factory := &trackingStoreFactory{}
	result := ownershipStoreLoadTask().Run(context.Background(), Deps{Stores: factory, QuorumLog: &captureDurableQuorumLog{}})
	require.ErrorIs(t, result.Err, ch.ErrInvalidConfig)
	require.Nil(t, result.StoreLoad)
	require.Equal(t, int32(1), factory.requireLastHandle(t).closeCalls.Load())
}
