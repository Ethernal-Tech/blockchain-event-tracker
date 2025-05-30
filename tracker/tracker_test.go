package tracker

import (
	"context"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/Ethernal-Tech/blockchain-event-tracker/store"
	"github.com/Ethernal-Tech/ethgo"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var _ EventSubscriber = (*mockEventSubscriber)(nil)

type mockEventSubscriber struct {
	logs []*ethgo.Log
}

func (m *mockEventSubscriber) AddLog(chainID *big.Int, log *ethgo.Log) error {
	m.logs = append(m.logs, log)

	return nil
}

var _ BlockProvider = (*mockProvider)(nil)

type mockProvider struct {
	mock.Mock

	blocks map[uint64]*ethgo.Block
	logs   []*ethgo.Log
}

// GetBlockByHash implements tracker.Provider.
func (m *mockProvider) GetBlockByHash(hash ethgo.Hash, full bool) (*ethgo.Block, error) {
	args := m.Called(hash, full)

	return1 := args.Get(0)

	if return1 != nil {
		return return1.(*ethgo.Block), args.Error(1) //nolint:forcetypeassert
	}

	return nil, args.Error(1)
}

// GetBlockByNumber implements tracker.Provider.
func (m *mockProvider) GetBlockByNumber(i ethgo.BlockNumber, full bool) (*ethgo.Block, error) {
	args := m.Called(i, full)

	if m.blocks != nil {
		return m.blocks[uint64(i)], nil
	}

	return1 := args.Get(0)

	if return1 != nil {
		return return1.(*ethgo.Block), args.Error(1) //nolint:forcetypeassert
	}

	return nil, args.Error(1)
}

// GetLogs implements tracker.Provider.
func (m *mockProvider) GetLogs(filter *ethgo.LogFilter) ([]*ethgo.Log, error) {
	args := m.Called(filter)

	if len(m.logs) > 0 {
		returnLog := m.logs[0]
		m.logs = m.logs[1:]

		return []*ethgo.Log{returnLog}, nil
	}

	return1 := args.Get(0)

	if return1 != nil {
		return return1.([]*ethgo.Log), args.Error(1) //nolint:forcetypeassert
	}

	return nil, args.Error(1)
}

// ChainID implements tracker.Provider.
func (m *mockProvider) ChainID() (*big.Int, error) {
	args := m.Called()

	return1 := args.Get(0)

	if return1 != nil {
		return return1.(*big.Int), args.Error(1) //nolint:forcetypeassert
	}

	return nil, args.Error(1)
}

func TestEventTracker_TrackBlock(t *testing.T) {
	t.Parallel()

	t.Run("Add block by block - no confirmed blocks", func(t *testing.T) {
		t.Parallel()

		tracker, err := NewEventTracker(createTestTrackerConfig(t, 10, 10, 0, nil), store.NewTestTrackerStore(t))

		require.NoError(t, err)

		// add some blocks, but don't go to confirmation level
		for i := uint64(1); i <= tracker.config.NumBlockConfirmations; i++ {
			require.NoError(t, tracker.trackBlock(context.Background(),
				&ethgo.Block{
					Number:     i,
					Hash:       ethgo.Hash{byte(i)},
					ParentHash: ethgo.Hash{byte(i - 1)},
				}))
		}

		// check that we have correct number of cached blocks
		require.Len(t, tracker.blockContainer.blocks, int(tracker.config.NumBlockConfirmations))
		require.Len(t, tracker.blockContainer.numToHashMap, int(tracker.config.NumBlockConfirmations))

		// check that we have no confirmed blocks
		require.Nil(t, tracker.blockContainer.GetConfirmedBlocks(tracker.config.NumBlockConfirmations))

		// check that the last processed block is 0, since we did not have any confirmed blocks
		require.Equal(t, uint64(0), tracker.blockContainer.LastProcessedBlockLocked())
		lastProcessedBlockInStore, err := tracker.store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, uint64(0), lastProcessedBlockInStore)

		// check that the last cached block is as expected
		require.Equal(t, tracker.config.NumBlockConfirmations, tracker.blockContainer.LastCachedBlock())
	})

	t.Run("Add block by block - have confirmed blocks - no logs in them - invalid subscriber", func(t *testing.T) {
		t.Parallel()

		numBlockConfirmations := uint64(3)

		// mock logs return so that no confirmed block has any logs we need
		blockProviderMock := new(mockProvider)
		blockProviderMock.On("ChainID").Return(big.NewInt(1), nil).Once()
		blockProviderMock.On("GetLogs", mock.Anything).Return([]*ethgo.Log{}, nil).Once()

		// create a tracker with invalid subscriber
		_, err := NewEventTracker(createTestTrackerConfigInvalidSub(t, numBlockConfirmations, 10, 0),
			store.NewTestTrackerStore(t))
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid configuration, event subscriber not set")
	})

	t.Run("Add block by block - have confirmed blocks - no logs in them", func(t *testing.T) {
		t.Parallel()

		numBlockConfirmations := uint64(3)
		totalNumOfPreCachedBlocks := numBlockConfirmations + 1
		numOfConfirmedBlocks := totalNumOfPreCachedBlocks - numBlockConfirmations + 1

		// mock logs return so that no confirmed block has any logs we need
		blockProviderMock := new(mockProvider)
		blockProviderMock.On("GetLogs", mock.Anything).Return([]*ethgo.Log{}, nil).Once()

		tracker, err := NewEventTracker(createTestTrackerConfig(t, numBlockConfirmations, 10, 0, blockProviderMock),
			store.NewTestTrackerStore(t))
		require.NoError(t, err)

		// add some blocks
		var block *ethgo.Block
		for i := uint64(1); i <= totalNumOfPreCachedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			}
			require.NoError(t, tracker.blockContainer.AddBlock(block))
		}

		// check that we have correct number of cached blocks
		require.Len(t, tracker.blockContainer.blocks, int(totalNumOfPreCachedBlocks))
		require.Len(t, tracker.blockContainer.numToHashMap, int(totalNumOfPreCachedBlocks))

		// track new block
		latestBlock := &ethgo.Block{
			Number:     block.Number + 1,
			Hash:       ethgo.Hash{byte(block.Number + 1)},
			ParentHash: block.Hash,
		}
		require.NoError(t, tracker.trackBlock(context.Background(), latestBlock))

		// check if the last cached block is as expected
		require.Equal(t, latestBlock.Number, tracker.blockContainer.LastCachedBlock())
		// check if the last confirmed block processed is as expected
		require.Equal(t, numOfConfirmedBlocks, tracker.blockContainer.LastProcessedBlock())
		// check if the last confirmed block is saved in db as well
		lastProcessedConfirmedBlock, err := tracker.store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, numOfConfirmedBlocks, lastProcessedConfirmedBlock)
		// check that in memory cache removed processed confirmed logs
		expectedNumOfBlocksInCache := totalNumOfPreCachedBlocks + 1 - numOfConfirmedBlocks
		require.Len(t, tracker.blockContainer.blocks, int(expectedNumOfBlocksInCache))
		require.Len(t, tracker.blockContainer.numToHashMap, int(expectedNumOfBlocksInCache))

		for i := uint64(1); i <= numOfConfirmedBlocks; i++ {
			_, exists := tracker.blockContainer.numToHashMap[i]
			require.False(t, exists)
			require.Equal(t, -1, tracker.blockContainer.indexOf(i))
		}

		blockProviderMock.AssertExpectations(t)
	})

	t.Run("Add block by block - have confirmed blocks with logs", func(t *testing.T) {
		t.Parallel()

		numBlockConfirmations := uint64(3)
		totalNumOfPreCachedBlocks := numBlockConfirmations + 1
		numOfConfirmedBlocks := totalNumOfPreCachedBlocks - numBlockConfirmations + 1

		// mock logs return so that no confirmed block has any logs we need
		logs := []*ethgo.Log{
			store.CreateTestLogForStateSyncEvent(t, 1, 1),
			store.CreateTestLogForStateSyncEvent(t, 1, 11),
			store.CreateTestLogForStateSyncEvent(t, 2, 3),
		}
		blockProviderMock := new(mockProvider)
		blockProviderMock.On("GetLogs", mock.Anything).Return(logs, nil).Once()

		tracker, err := NewEventTracker(createTestTrackerConfig(t, numBlockConfirmations, 10, 0, blockProviderMock),
			store.NewTestTrackerStore(t))
		require.NoError(t, err)

		// add some blocks
		var block *ethgo.Block
		for i := uint64(1); i <= totalNumOfPreCachedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			}
			require.NoError(t, tracker.blockContainer.AddBlock(block))
		}

		// check that we have correct number of cached blocks
		require.Len(t, tracker.blockContainer.blocks, int(totalNumOfPreCachedBlocks))
		require.Len(t, tracker.blockContainer.numToHashMap, int(totalNumOfPreCachedBlocks))

		// track new block
		latestBlock := &ethgo.Block{
			Number:     block.Number + 1,
			Hash:       ethgo.Hash{byte(block.Number + 1)},
			ParentHash: block.Hash,
		}
		require.NoError(t, tracker.trackBlock(context.Background(), latestBlock))

		// check if the last cached block is as expected
		require.Equal(t, latestBlock.Number, tracker.blockContainer.LastCachedBlock())
		// check if the last confirmed block processed is as expected
		require.Equal(t, numOfConfirmedBlocks, tracker.blockContainer.LastProcessedBlock())
		// check if the last confirmed block is saved in db as well
		lastProcessedConfirmedBlock, err := tracker.store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, numOfConfirmedBlocks, lastProcessedConfirmedBlock)
		// check if we have logs in store
		for _, log := range logs {
			logFromDB, err := tracker.store.GetLog(log.BlockNumber, log.LogIndex)
			require.NoError(t, err)
			require.Equal(t, log.Address, logFromDB.Address)
			require.Equal(t, log.BlockNumber, log.BlockNumber)
			require.Equal(t, log.LogIndex, logFromDB.LogIndex)
		}
		// check that in memory cache removed processed confirmed logs
		expectedNumOfBlocksInCache := totalNumOfPreCachedBlocks + 1 - numOfConfirmedBlocks
		require.Len(t, tracker.blockContainer.blocks, int(expectedNumOfBlocksInCache))
		require.Len(t, tracker.blockContainer.numToHashMap, int(expectedNumOfBlocksInCache))

		for i := uint64(1); i <= numOfConfirmedBlocks; i++ {
			_, exists := tracker.blockContainer.numToHashMap[i]
			require.False(t, exists)
			require.Equal(t, -1, tracker.blockContainer.indexOf(i))
		}

		blockProviderMock.AssertExpectations(t)
	})

	t.Run("Add block by block - have confirmed blocks with logs - invalid subscriber", func(t *testing.T) {
		t.Parallel()

		numBlockConfirmations := uint64(3)

		// mock logs return so that no confirmed block has any logs we need
		logs := []*ethgo.Log{
			store.CreateTestLogForStateSyncEvent(t, 1, 1),
			store.CreateTestLogForStateSyncEvent(t, 1, 11),
			store.CreateTestLogForStateSyncEvent(t, 2, 3),
		}
		blockProviderMock := new(mockProvider)
		blockProviderMock.On("GetLogs", mock.Anything).Return(logs, nil).Once()

		// create a tracker with invalid subscriber
		_, err := NewEventTracker(createTestTrackerConfigInvalidSub(t, numBlockConfirmations, 10, 0),
			store.NewTestTrackerStore(t))
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid configuration, event subscriber not set")
	})

	t.Run("Add block by block - an error occurs on getting logs", func(t *testing.T) {
		t.Parallel()

		numBlockConfirmations := uint64(3)
		totalNumOfPreCachedBlocks := numBlockConfirmations + 1

		// mock logs return so that no confirmed block has any logs we need
		blockProviderMock := new(mockProvider)
		blockProviderMock.On("GetLogs", mock.Anything).Return(nil, errors.New("some error occurred")).Once()

		tracker, err := NewEventTracker(createTestTrackerConfig(t, numBlockConfirmations, 10, 0, blockProviderMock),
			store.NewTestTrackerStore(t))
		require.NoError(t, err)

		// add some blocks
		var block *ethgo.Block
		for i := uint64(1); i <= totalNumOfPreCachedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			}
			require.NoError(t, tracker.blockContainer.AddBlock(block))
		}

		// check that we have correct number of cached blocks
		require.Len(t, tracker.blockContainer.blocks, int(totalNumOfPreCachedBlocks))
		require.Len(t, tracker.blockContainer.numToHashMap, int(totalNumOfPreCachedBlocks))

		// track new block
		latestBlock := &ethgo.Block{
			Number:     block.Number + 1,
			Hash:       ethgo.Hash{byte(block.Number + 1)},
			ParentHash: block.Hash,
		}
		require.ErrorContains(t, tracker.trackBlock(context.Background(), latestBlock), "some error occurred")

		// check if the last cached block is as expected
		require.Equal(t, latestBlock.Number, tracker.blockContainer.LastCachedBlock())
		// check if the last confirmed block processed is as expected, in this case 0, because an error occurred
		require.Equal(t, uint64(0), tracker.blockContainer.LastProcessedBlock())
		// check if the last confirmed block is saved in db as well
		lastProcessedConfirmedBlock, err := tracker.store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, uint64(0), lastProcessedConfirmedBlock)
		// check that in memory cache nothing got removed, and that we have the latest block as well
		expectedNumOfBlocksInCache := totalNumOfPreCachedBlocks + 1 // because of the latest block
		require.Len(t, tracker.blockContainer.blocks, int(expectedNumOfBlocksInCache))
		require.Len(t, tracker.blockContainer.numToHashMap, int(expectedNumOfBlocksInCache))

		blockProviderMock.AssertExpectations(t)
	})

	t.Run("Starting tracker - sync up in batches", func(t *testing.T) {
		t.Parallel()

		batchSize := uint64(4)
		numBlockConfirmations := uint64(3)
		numOfMissedBlocks := batchSize * 2

		blockProviderMock := &mockProvider{blocks: make(map[uint64]*ethgo.Block)}

		// mock logs return so that no confirmed block has any logs we need
		logs := []*ethgo.Log{
			store.CreateTestLogForStateSyncEvent(t, 1, 1),
			store.CreateTestLogForStateSyncEvent(t, 2, 3),
			store.CreateTestLogForStateSyncEvent(t, 6, 11),
		}
		blockProviderMock.logs = logs
		// we will have three groups of confirmed blocks
		// syncing blocks: 1, 2, 3, 4, 5, 6, 7, 8, 9
		// first batch of gotten blocks: 1, 2, 3, 4 - confirmed blocks: 1
		// second batch of gotten blocks: 5, 6, 7, 8 - confirmed blocks: 2, 3, 4, 5
		// process the latest block as well (block 9) - confirmed blocks: 6
		// just mock the call, it will use the provider.logs map to handle proper returns
		blockProviderMock.On("GetLogs", mock.Anything).Return(nil, nil).Times(len(logs))
		// just mock the call, it will use the provider.blocks map to handle proper returns
		blockProviderMock.On("GetBlockByNumber", mock.Anything, mock.Anything).Return(nil, nil).Times(int(numOfMissedBlocks))

		tracker, err := NewEventTracker(createTestTrackerConfig(t, numBlockConfirmations, batchSize, 0, blockProviderMock),
			store.NewTestTrackerStore(t))
		require.NoError(t, err)

		// mock getting missed blocks
		var block *ethgo.Block
		for i := uint64(1); i <= numOfMissedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			}
			blockProviderMock.blocks[i] = block
		}

		// check that initially we don't have anything cached
		require.Len(t, tracker.blockContainer.blocks, 0)
		require.Len(t, tracker.blockContainer.numToHashMap, 0)

		// track new block
		latestBlock := &ethgo.Block{
			Number:     block.Number + 1,
			Hash:       ethgo.Hash{byte(block.Number + 1)},
			ParentHash: block.Hash,
		}
		require.NoError(t, tracker.trackBlock(context.Background(), latestBlock))

		// check if the last cached block is as expected
		require.Equal(t, latestBlock.Number, tracker.blockContainer.LastCachedBlock())
		// check if the last confirmed block processed is as expected
		expectedLastProcessed := numOfMissedBlocks + 1 - numBlockConfirmations
		require.Equal(t, expectedLastProcessed, tracker.blockContainer.LastProcessedBlock())
		// check if the last confirmed block is saved in db as well
		lastProcessedConfirmedBlock, err := tracker.store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, expectedLastProcessed, lastProcessedConfirmedBlock)
		// check if we have logs in store
		logsFromDB, err := tracker.store.GetAllLogs()
		require.NoError(t, err)
		require.Len(t, logsFromDB, len(logs))

		// check that in memory cache removed processed confirmed logs
		require.Len(t, tracker.blockContainer.blocks, int(numOfMissedBlocks+1-expectedLastProcessed))
		require.Len(t, tracker.blockContainer.numToHashMap, int(numOfMissedBlocks+1-expectedLastProcessed))

		for i := expectedLastProcessed + 1; i <= numOfMissedBlocks+1; i++ {
			_, exists := tracker.blockContainer.numToHashMap[i]
			require.True(t, exists)
			require.Equal(t, i, tracker.blockContainer.blocks[i-expectedLastProcessed-1])
		}

		blockProviderMock.AssertExpectations(t)
	})

	t.Run("Sync up in batches - have cached blocks - no reorgs", func(t *testing.T) {
		t.Parallel()

		batchSize := uint64(4)
		numBlockConfirmations := uint64(3)
		numOfMissedBlocks := batchSize * 4
		numOfCachedBlocks := uint64(4)

		blockProviderMock := &mockProvider{blocks: make(map[uint64]*ethgo.Block)}

		// mock logs return so that no confirmed block has any logs we need
		logs := []*ethgo.Log{
			store.CreateTestLogForStateSyncEvent(t, 1, 1),
			store.CreateTestLogForStateSyncEvent(t, 2, 3),
			store.CreateTestLogForStateSyncEvent(t, 6, 11),
			store.CreateTestLogForStateSyncEvent(t, 10, 1),
		}
		// we will have three groups of confirmed blocks
		// have cached blocks, 1, 2, 3, 4
		// cleans state
		// syncing blocks: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12
		// first batch of gotten blocks: 1, 2, 3, 4 - confirmed blocks: 1
		// second batch of gotten blocks: 5, 6, 7, 8 - confirmed blocks: 2, 3, 4, 5
		// third batch of gotten blocks: 9, 10, 11, 12 - confirmed blocks: 6, 7, 8, 9
		// process the latest block as well (block 13) - confirmed blocks: 10
		// just mock the call, it will use the provider.logs map to handle proper returns
		blockProviderMock.On("GetLogs", mock.Anything).Return(logs[0:1], nil).Once()
		blockProviderMock.On("GetLogs", mock.Anything).Return(logs[1:2], nil).Once()
		blockProviderMock.On("GetLogs", mock.Anything).Return(logs[2:3], nil).Once()
		blockProviderMock.On("GetLogs", mock.Anything).Return(logs[3:], nil).Once()
		blockProviderMock.On("GetLogs", mock.Anything).Return(nil, nil).Times(2)
		// just mock the call, it will use the provider.blocks map to handle proper returns
		blockProviderMock.On("GetBlockByNumber", mock.Anything, mock.Anything).Return(nil, nil).Times(
			int(numOfMissedBlocks + numOfCachedBlocks))

		tracker, err := NewEventTracker(createTestTrackerConfig(t, numBlockConfirmations, batchSize, 0, blockProviderMock),
			store.NewTestTrackerStore(t))
		require.NoError(t, err)

		var block *ethgo.Block

		// add some cached blocks
		for i := uint64(1); i <= numOfCachedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			}

			blockProviderMock.blocks[i] = block
			require.NoError(t, tracker.blockContainer.AddBlock(block))
		}

		// check that initially we have some cached blocks
		require.Len(t, tracker.blockContainer.blocks, int(numOfCachedBlocks))
		require.Len(t, tracker.blockContainer.numToHashMap, int(numOfCachedBlocks))

		// mock getting missed blocks
		for i := numOfCachedBlocks + 1; i <= numOfMissedBlocks+numOfCachedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			}
			blockProviderMock.blocks[i] = block
		}

		// track new block
		latestBlock := &ethgo.Block{
			Number:     block.Number + 1,
			Hash:       ethgo.Hash{byte(block.Number + 1)},
			ParentHash: block.Hash,
		}
		require.NoError(t, tracker.trackBlock(context.Background(), latestBlock))

		// check if the last cached block is as expected
		require.Equal(t, latestBlock.Number, tracker.blockContainer.LastCachedBlock())
		// check if the last confirmed block processed is as expected
		expectedLastProcessed := numOfMissedBlocks + numOfCachedBlocks + 1 - numBlockConfirmations
		require.Equal(t, expectedLastProcessed, tracker.blockContainer.LastProcessedBlock())
		// check if the last confirmed block is saved in db as well
		lastProcessedConfirmedBlock, err := tracker.store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, expectedLastProcessed, lastProcessedConfirmedBlock)
		// check if we have logs in store
		logsFromDB, err := tracker.store.GetAllLogs()
		require.NoError(t, err)
		require.Len(t, logsFromDB, len(logs))

		// check that in memory cache removed processed confirmed logs
		expectedNumOfNonProcessedBlocks := int(numOfMissedBlocks + numOfCachedBlocks + 1 - expectedLastProcessed)
		require.Len(t, tracker.blockContainer.blocks, expectedNumOfNonProcessedBlocks)
		require.Len(t, tracker.blockContainer.numToHashMap, expectedNumOfNonProcessedBlocks)

		for i := expectedLastProcessed + 1; i <= numOfMissedBlocks+1; i++ {
			_, exists := tracker.blockContainer.numToHashMap[i]
			require.True(t, exists)
			require.Equal(t, i, tracker.blockContainer.blocks[i-expectedLastProcessed-1])
		}

		blockProviderMock.AssertExpectations(t)
	})

	t.Run("Sync up in batches - have cached blocks - a reorg happened", func(t *testing.T) {
		t.Parallel()

		batchSize := uint64(4)
		numBlockConfirmations := uint64(3)
		numOfCachedBlocks := uint64(4)

		blockProviderMock := &mockProvider{blocks: make(map[uint64]*ethgo.Block)}

		// mock logs return so that no confirmed block has any logs we need
		logs := []*ethgo.Log{
			store.CreateTestLogForStateSyncEvent(t, 1, 1),
			store.CreateTestLogForStateSyncEvent(t, 2, 3),
		}
		blockProviderMock.logs = logs
		// we will have 2 groups of confirmed blocks
		// have cached blocks, 1, 2, 3, 4
		// notice there was a reorg on block 5
		// cleans state
		// syncing blocks: 1, 2, 3, 4, 5
		// first batch of gotten blocks: 1, 2, 3, 4 - confirmed blocks: 1
		// process the latest block as well (block 5) - confirmed blocks: 2
		// just mock the call, it will use the provider.logs map to handle proper returns
		blockProviderMock.On("GetLogs", mock.Anything).Return(nil, nil).Times(len(logs))
		// just mock the call, it will use the provider.blocks map to handle proper returns
		blockProviderMock.On("GetBlockByNumber", mock.Anything, mock.Anything).Return(nil, nil).Times(int(numOfCachedBlocks))

		tracker, err := NewEventTracker(createTestTrackerConfig(t, numBlockConfirmations, batchSize, 0, blockProviderMock),
			store.NewTestTrackerStore(t))
		require.NoError(t, err)

		var block *ethgo.Block

		// add some cached blocks
		for i := uint64(1); i <= numOfCachedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i + numOfCachedBlocks)},
				ParentHash: ethgo.Hash{byte(i + numOfCachedBlocks - 1)},
			}
			require.NoError(t, tracker.blockContainer.AddBlock(block))
		}

		// check that initially we have some cached blocks
		require.Len(t, tracker.blockContainer.blocks, int(numOfCachedBlocks))
		require.Len(t, tracker.blockContainer.numToHashMap, int(numOfCachedBlocks))

		// mock getting new state
		for i := uint64(1); i <= numOfCachedBlocks; i++ {
			block = &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			}
			blockProviderMock.blocks[i] = block
		}

		// track new block
		latestBlock := &ethgo.Block{
			Number:     block.Number + 1,
			Hash:       ethgo.Hash{byte(block.Number + 1)},
			ParentHash: block.Hash,
		}
		require.NoError(t, tracker.trackBlock(context.Background(), latestBlock))

		// check if the last cached block is as expected
		require.Equal(t, latestBlock.Number, tracker.blockContainer.LastCachedBlock())
		// check if the last confirmed block processed is as expected
		expectedLastProcessed := numOfCachedBlocks + 1 - numBlockConfirmations
		require.Equal(t, expectedLastProcessed, tracker.blockContainer.LastProcessedBlock())
		// check if the last confirmed block is saved in db as well
		lastProcessedConfirmedBlock, err := tracker.store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, expectedLastProcessed, lastProcessedConfirmedBlock)
		// check if we have logs in store
		logsFromDB, err := tracker.store.GetAllLogs()
		require.NoError(t, err)
		require.Len(t, logsFromDB, len(logs))

		// check that in memory cache removed processed confirmed logs
		expectedNumOfNonProcessedBlocks := int(numOfCachedBlocks + 1 - expectedLastProcessed)
		require.Len(t, tracker.blockContainer.blocks, expectedNumOfNonProcessedBlocks)
		require.Len(t, tracker.blockContainer.numToHashMap, expectedNumOfNonProcessedBlocks)

		for i := expectedLastProcessed + 1; i <= numOfCachedBlocks+1; i++ {
			_, exists := tracker.blockContainer.numToHashMap[i]
			require.True(t, exists)
			require.Equal(t, i, tracker.blockContainer.blocks[i-expectedLastProcessed-1])
		}

		blockProviderMock.AssertExpectations(t)
	})

	t.Run("Indexer is in the future", func(t *testing.T) {
		t.Parallel()

		const (
			batchSize             = uint64(4)
			numBlockConfirmations = uint64(3)
			numOfCachedBlocks     = uint64(4)
		)

		config := createTestTrackerConfig(t, numBlockConfirmations, batchSize, 0, new(mockProvider))
		config.StartBlockFromGenesis = 1000

		tracker, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.NoError(t, err)

		// mock getting new state
		for i := uint64(1); i <= numOfCachedBlocks; i++ {
			tracker.blockContainer.blocks = append(tracker.blockContainer.blocks, i)
			tracker.blockContainer.numToHashMap[i] = ethgo.Hash{byte(i)}
		}

		for i := numOfCachedBlocks; i <= numOfCachedBlocks+2; i++ {
			lastProcessedBlock := numOfCachedBlocks + 1 // on last iteration we test getNewState
			if i == numOfCachedBlocks+2 {
				lastProcessedBlock = i
			}

			tracker.blockContainer.lastProcessedConfirmedBlock = lastProcessedBlock

			err = tracker.trackBlock(context.Background(), &ethgo.Block{
				Number:     i,
				Hash:       ethgo.Hash{byte(i)},
				ParentHash: ethgo.Hash{byte(i - 1)},
			})

			require.NoError(t, err)
			require.Equal(t, lastProcessedBlock, tracker.blockContainer.LastProcessedBlock())
			require.Equal(t, numOfCachedBlocks, tracker.blockContainer.LastCachedBlock())
		}
	})

	t.Run("Create a tracker - invalid/default store", func(t *testing.T) {
		t.Parallel()

		batchSize := uint64(4)
		numBlockConfirmations := uint64(3)

		_, err := NewEventTracker(createTestTrackerConfig(t, numBlockConfirmations, batchSize, 0, nil), nil)
		require.NoError(t, err)

		// Remove default.db file created during test
		if _, err = os.Stat(defaultStore); err == nil {
			os.RemoveAll(defaultStore)
		}
	})

	t.Run("Create a tracker with invalid config", func(t *testing.T) {
		t.Parallel()

		_, err := NewEventTracker(nil, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid configuration")
	})

	t.Run("Create a tracker with invalid config, missing logger", func(t *testing.T) {
		t.Parallel()

		batchSize := uint64(4)
		numBlockConfirmations := uint64(3)

		config := createTestTrackerConfig(t, numBlockConfirmations, batchSize, 0, nil)
		config.Logger = nil

		_, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.NoError(t, err)
	})
}

func createTestTrackerConfig(t *testing.T,
	numBlockConfirmations, batchSize, numOfBlocksToReconcile uint64, blockProviderMock *mockProvider) *EventTrackerConfig {
	t.Helper()

	if blockProviderMock == nil {
		blockProviderMock = new(mockProvider)
	}

	blockProviderMock.On("ChainID").Return(big.NewInt(1), nil).Once()

	return &EventTrackerConfig{
		RPCEndpoint:            "http://some-rpc-url.com",
		NumBlockConfirmations:  numBlockConfirmations,
		SyncBatchSize:          batchSize,
		NumOfBlocksToReconcile: numOfBlocksToReconcile,
		PollInterval:           2 * time.Second,
		Logger:                 hclog.NewNullLogger(),
		LogFilter: map[ethgo.Address][]ethgo.Hash{
			ethgo.ZeroAddress: {store.StateSyncEventABI.ID()},
		},
		EventSubscriber: new(mockEventSubscriber),
		BlockProvider:   blockProviderMock,
	}
}

func createTestTrackerConfigInvalidSub(t *testing.T,
	numBlockConfirmations, batchSize, numOfBlocksToReconcile uint64) *EventTrackerConfig {
	t.Helper()

	return &EventTrackerConfig{
		RPCEndpoint:            "http://some-rpc-url.com",
		NumBlockConfirmations:  numBlockConfirmations,
		SyncBatchSize:          batchSize,
		NumOfBlocksToReconcile: numOfBlocksToReconcile,
		PollInterval:           2 * time.Second,
		Logger:                 hclog.NewNullLogger(),
		LogFilter: map[ethgo.Address][]ethgo.Hash{
			ethgo.ZeroAddress: {store.StateSyncEventABI.ID()},
		},
		EventSubscriber: nil,
		BlockProvider:   new(mockProvider),
	}
}
