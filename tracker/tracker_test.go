package tracker

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/Ethernal-Tech/blockchain-event-tracker/store"
	storeMocks "github.com/Ethernal-Tech/blockchain-event-tracker/store/mocks"
	"github.com/Ethernal-Tech/blockchain-event-tracker/tracker/mocks"
	"github.com/Ethernal-Tech/ethgo"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestWaitForNewFinalizedBlock_ContextCancelled(t *testing.T) {
	tracker := &EventTracker{
		config: &EventTrackerConfig{
			PollInterval:    100 * time.Millisecond,
			BlockProvider:   mocks.NewBlockProvider(t),
			EventSubscriber: mocks.NewEventSubscriber(t),
			Logger:          hclog.NewNullLogger(),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lastSeenBlock := uint64(10)
	result := tracker.waitForNewFinalizedBlock(ctx, lastSeenBlock)
	require.Equal(t, lastSeenBlock, result)
}

func TestWaitForNewFinalizedBlock_NewBlockFound(t *testing.T) {
	blockProviderMock := mocks.NewBlockProvider(t)
	eventSubscriberMock := mocks.NewEventSubscriber(t)

	tracker := &EventTracker{
		config: &EventTrackerConfig{
			PollInterval:    100 * time.Millisecond,
			BlockProvider:   blockProviderMock,
			EventSubscriber: eventSubscriberMock,
			Logger:          hclog.NewNullLogger(),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lastSeenBlock := uint64(10)
	newBlock := &ethgo.Block{Number: uint64(11)}

	blockProviderMock.On("GetBlockByNumber", ethgo.Finalized, false).Return(newBlock, nil).Once()

	result := tracker.waitForNewFinalizedBlock(ctx, lastSeenBlock)
	require.Equal(t, newBlock.Number, result)
}

func TestWaitForNewFinalizedBlock_ErrorGettingBlock(t *testing.T) {
	blockProviderMock := mocks.NewBlockProvider(t)
	eventSubscriberMock := mocks.NewEventSubscriber(t)

	tracker := &EventTracker{
		config: &EventTrackerConfig{
			PollInterval:    100 * time.Millisecond,
			BlockProvider:   blockProviderMock,
			EventSubscriber: eventSubscriberMock,
			Logger:          hclog.NewNullLogger(),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lastSeenBlock := uint64(10)

	blockProviderMock.On("GetBlockByNumber", ethgo.Finalized, false).Return(nil, fmt.Errorf("error")).Twice()

	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	result := tracker.waitForNewFinalizedBlock(ctx, lastSeenBlock)
	require.Equal(t, lastSeenBlock, result)
}

func TestGetBlockRange_Success(t *testing.T) {
	blockProviderMock := mocks.NewBlockProvider(t)
	eventSubscriberMock := mocks.NewEventSubscriber(t)
	storeMock := storeMocks.NewEventTrackerStore(t)

	tracker := &EventTracker{
		config: &EventTrackerConfig{
			BlockProvider:   blockProviderMock,
			EventSubscriber: eventSubscriberMock,
			LogFilter: map[ethgo.Address][]ethgo.Hash{
				{0x1}: {{0x1}},
			},
			Logger: hclog.NewNullLogger(),
		},
		store: storeMock,
	}

	fromBlock := uint64(1)
	toBlock := uint64(10)
	logs := []*ethgo.Log{
		{
			Address: ethgo.Address{0x1},
			Topics:  []ethgo.Hash{{0x1}},
		},
	}

	blockProviderMock.On("GetLogs", tracker.getLogsQuery(fromBlock, toBlock)).Return(logs, nil).Once()
	storeMock.On("InsertLastProcessedBlock", toBlock).Return(nil).Once()
	storeMock.On("InsertLogs", logs).Return(nil).Once()
	eventSubscriberMock.On("AddLog", tracker.chainID, logs[0]).Return(nil).Once()

	err := tracker.getBlockRange(fromBlock, toBlock)
	require.NoError(t, err)
}

func TestGetBlockRange(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		fromBlock uint64
		toBlock   uint64
		mockFn    func(
			*EventTracker,
			*mocks.BlockProvider,
			*storeMocks.EventTrackerStore,
			*mocks.EventSubscriber,
		)
		expectedError string
	}{
		{
			name:      "success",
			fromBlock: 1,
			toBlock:   100,
			mockFn: func(
				tracker *EventTracker,
				blockProviderMock *mocks.BlockProvider,
				storeMock *storeMocks.EventTrackerStore,
				eventSubscriberMock *mocks.EventSubscriber) {
				tracker.config.LogFilter[ethgo.Address{0x1}] = []ethgo.Hash{{0x1}}
				logs := []*ethgo.Log{
					{
						Address: ethgo.Address{0x1},
						Topics:  []ethgo.Hash{{0x1}},
					},
				}

				blockProviderMock.On("GetLogs", tracker.getLogsQuery(1, 100)).Return(logs, nil).Once()
				storeMock.On("InsertLastProcessedBlock", uint64(100)).Return(nil).Once()
				storeMock.On("InsertLogs", logs).Return(nil).Once()
				eventSubscriberMock.On("AddLog", mock.Anything, logs[0]).Return(nil).Once()
			},
		},
		{
			name:      "insert logs error",
			fromBlock: 11,
			toBlock:   20,
			mockFn: func(
				tracker *EventTracker,
				blockProviderMock *mocks.BlockProvider,
				storeMock *storeMocks.EventTrackerStore,
				eventSubscriberMock *mocks.EventSubscriber) {
				blockProviderMock.On("GetLogs", tracker.getLogsQuery(11, 20)).Return([]*ethgo.Log{}, nil).Once()
				storeMock.On("InsertLastProcessedBlock", uint64(20)).Return(nil).Once()
				storeMock.On("InsertLogs", mock.Anything).Return(errors.New("test error")).Once()
			},
			expectedError: "test error",
		},
		{
			name:      "insert last processed block error",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func(
				tracker *EventTracker,
				blockProviderMock *mocks.BlockProvider,
				storeMock *storeMocks.EventTrackerStore,
				eventSubscriberMock *mocks.EventSubscriber) {
				blockProviderMock.On("GetLogs", mock.Anything).Return([]*ethgo.Log{}, nil).Once()
				storeMock.On("InsertLastProcessedBlock", mock.Anything).Return(errors.New("test error")).Once()
			},
			expectedError: "test error",
		},
		{
			name:      "get logs error",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func(
				tracker *EventTracker,
				blockProviderMock *mocks.BlockProvider,
				storeMock *storeMocks.EventTrackerStore,
				eventSubscriberMock *mocks.EventSubscriber) {
				blockProviderMock.On("GetLogs", tracker.getLogsQuery(1, 10)).Return(nil, errors.New("test error")).Once()
			},
			expectedError: "test error",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blockProviderMock := mocks.NewBlockProvider(t)
			eventSubscriberMock := mocks.NewEventSubscriber(t)
			storeMock := storeMocks.NewEventTrackerStore(t)

			tracker := &EventTracker{
				config: &EventTrackerConfig{
					BlockProvider:   blockProviderMock,
					EventSubscriber: eventSubscriberMock,
					LogFilter:       map[ethgo.Address][]ethgo.Hash{},
					Logger:          hclog.NewNullLogger(),
				},
				store: storeMock,
			}

			tc.mockFn(tracker, blockProviderMock, storeMock, eventSubscriberMock)

			if tc.expectedError != "" {
				require.ErrorContains(t, tracker.getBlockRange(tc.fromBlock, tc.toBlock), tc.expectedError)
			} else {
				require.NoError(t, tracker.getBlockRange(tc.fromBlock, tc.toBlock))
			}

			storeMock.AssertExpectations(t)
			blockProviderMock.AssertExpectations(t)
			eventSubscriberMock.AssertExpectations(t)
		})
	}
}

func TestNewEventTracker(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		startBlockFromGenesis uint64
		mockFn                func() (*EventTrackerConfig, *mocks.BlockProvider,
			*mocks.EventSubscriber, *storeMocks.EventTrackerStore)
		expectedError      string
		expectedStartBlock uint64
	}{
		{
			name:                  "nil config",
			startBlockFromGenesis: 0,
			mockFn: func() (*EventTrackerConfig, *mocks.BlockProvider, *mocks.EventSubscriber, *storeMocks.EventTrackerStore) {
				return nil, nil, nil, nil
			},
			expectedError: "invalid configuration. Failed to init Event Tracker",
		},
		{
			name: "nil event subscriber",
			mockFn: func() (*EventTrackerConfig, *mocks.BlockProvider,
				*mocks.EventSubscriber, *storeMocks.EventTrackerStore) {
				cfg := &EventTrackerConfig{
					Logger: hclog.NewNullLogger(),
				}

				return cfg, nil, nil, nil
			},
			startBlockFromGenesis: 0,
			expectedError:         "invalid configuration, event subscriber not set. Failed to init Event Tracker",
		},
		{
			name: "successful initialization with provided store - " +
				"last processed block lower than genesis block",
			startBlockFromGenesis: 10,
			mockFn: func() (*EventTrackerConfig, *mocks.BlockProvider,
				*mocks.EventSubscriber, *storeMocks.EventTrackerStore) {
				storeMock := storeMocks.NewEventTrackerStore(t)
				blockProviderMock := mocks.NewBlockProvider(t)
				eventSubscriberMock := mocks.NewEventSubscriber(t)

				cfg := &EventTrackerConfig{
					EventSubscriber: eventSubscriberMock,
					BlockProvider:   blockProviderMock,
				}

				blockProviderMock.On("ChainID").Return(big.NewInt(1), nil).Once()
				storeMock.On("GetLastProcessedBlock").Return(uint64(5), nil).Once()
				storeMock.On("InsertLastProcessedBlock", uint64(10)).Return(nil).Once()

				return cfg, blockProviderMock, eventSubscriberMock, storeMock
			},
		},
		{
			name:                  "successful initialization with default store",
			startBlockFromGenesis: 10,
			mockFn: func() (*EventTrackerConfig, *mocks.BlockProvider,
				*mocks.EventSubscriber, *storeMocks.EventTrackerStore) {
				blockProviderMock := mocks.NewBlockProvider(t)
				eventSubscriberMock := mocks.NewEventSubscriber(t)
				cfg := &EventTrackerConfig{
					EventSubscriber: eventSubscriberMock,
					BlockProvider:   blockProviderMock,
				}

				blockProviderMock.On("ChainID").Return(big.NewInt(1), nil).Once()

				return cfg, blockProviderMock, eventSubscriberMock, nil
			},
			expectedStartBlock: 10,
		},
		{
			name:                  "error getting chain ID",
			startBlockFromGenesis: 10,
			mockFn: func() (*EventTrackerConfig, *mocks.BlockProvider,
				*mocks.EventSubscriber, *storeMocks.EventTrackerStore) {
				blockProviderMock := mocks.NewBlockProvider(t)
				eventSubscriberMock := mocks.NewEventSubscriber(t)
				storeMock := storeMocks.NewEventTrackerStore(t)
				cfg := &EventTrackerConfig{
					EventSubscriber: eventSubscriberMock,
					BlockProvider:   blockProviderMock,
				}

				blockProviderMock.On("ChainID").Return(nil, errors.New("test error")).Once()

				return cfg, blockProviderMock, eventSubscriberMock, storeMock
			},
			expectedError: "test error",
		},
		{
			name:                  "error getting last processed block",
			startBlockFromGenesis: 100,
			mockFn: func() (*EventTrackerConfig, *mocks.BlockProvider,
				*mocks.EventSubscriber, *storeMocks.EventTrackerStore) {
				blockProviderMock := mocks.NewBlockProvider(t)
				eventSubscriberMock := mocks.NewEventSubscriber(t)
				storeMock := storeMocks.NewEventTrackerStore(t)
				cfg := &EventTrackerConfig{
					EventSubscriber: eventSubscriberMock,
					BlockProvider:   blockProviderMock,
				}

				blockProviderMock.On("ChainID").Return(big.NewInt(1), nil).Once()
				storeMock.On("GetLastProcessedBlock").Return(uint64(0), errors.New("test error")).Once()

				return cfg, blockProviderMock, eventSubscriberMock, storeMock
			},
			expectedError: "error",
		},
		{
			name:                  "successful initialization with reconciliation",
			startBlockFromGenesis: 1,
			mockFn: func() (*EventTrackerConfig, *mocks.BlockProvider,
				*mocks.EventSubscriber, *storeMocks.EventTrackerStore) {
				blockProviderMock := mocks.NewBlockProvider(t)
				eventSubscriberMock := mocks.NewEventSubscriber(t)
				storeMock := storeMocks.NewEventTrackerStore(t)
				cfg := &EventTrackerConfig{
					EventSubscriber:        eventSubscriberMock,
					BlockProvider:          blockProviderMock,
					NumOfBlocksToReconcile: 100,
				}

				blockProviderMock.On("ChainID").Return(big.NewInt(1), nil).Once()
				blockProviderMock.On("GetBlockByNumber", ethgo.Finalized, false).Return(
					&ethgo.Block{Number: 1000}, nil).Once()
				storeMock.On("GetLastProcessedBlock").Return(uint64(0), nil).Once()
				storeMock.On("InsertLastProcessedBlock", uint64(900)).Return(nil).Once()

				return cfg, blockProviderMock, eventSubscriberMock, storeMock
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, blockProviderMock, eventSubscriberMock, storeMock := tc.mockFn()

			var store store.EventTrackerStore
			if storeMock != nil {
				// for some strange reason, if we pass storeMock as nil to the NewEventTracker function,
				// it won't recognize it as nil, and it won't instantiate the default store
				store = storeMock
			}

			tracker, err := NewEventTracker(cfg, store, tc.startBlockFromGenesis)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				require.Nil(t, tracker)
			} else {
				if storeMock == nil {
					lastProcessedBlock, err := tracker.store.GetLastProcessedBlock()
					require.NoError(t, err)
					require.Equal(t, tc.expectedStartBlock, lastProcessedBlock)
				}

				require.NoError(t, err)
			}

			if blockProviderMock != nil {
				blockProviderMock.AssertExpectations(t)
			}

			if eventSubscriberMock != nil {
				eventSubscriberMock.AssertExpectations(t)
			}

			if storeMock != nil {
				storeMock.AssertExpectations(t)
			}
		})
	}
}
