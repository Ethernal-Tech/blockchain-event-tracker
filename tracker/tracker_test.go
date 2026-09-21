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
	logs         []*ethgo.Log
	addLogErrors []error
	addLogCalls  int
}

func (m *mockEventSubscriber) AddLog(chainID *big.Int, log *ethgo.Log) error {
	callIndex := m.addLogCalls
	m.addLogCalls++

	if callIndex < len(m.addLogErrors) && m.addLogErrors[callIndex] != nil {
		return m.addLogErrors[callIndex]
	}

	m.logs = append(m.logs, log)

	return nil
}

var _ Provider = (*mockProvider)(nil)

type mockProvider struct {
	mock.Mock
}

// BlockNumber implements tracker.Provider.
func (m *mockProvider) BlockNumber() (uint64, error) {
	args := m.Called()

	return args.Get(0).(uint64), args.Error(1) //nolint:forcetypeassert
}

func matchesLogRange(fromBlock, toBlock uint64) func(*ethgo.LogFilter) bool {
	return func(filter *ethgo.LogFilter) bool {
		return filter.From != nil && uint64(*filter.From) == fromBlock &&
			filter.To != nil && uint64(*filter.To) == toBlock
	}
}

// GetBlockByNumber implements tracker.Provider.
func (m *mockProvider) GetBlockByNumber(i ethgo.BlockNumber, full bool) (*ethgo.Block, error) {
	args := m.Called(i, full)

	return1 := args.Get(0)

	if return1 != nil {
		return return1.(*ethgo.Block), args.Error(1) //nolint:forcetypeassert
	}

	return nil, args.Error(1)
}

// GetLogs implements tracker.Provider.
func (m *mockProvider) GetLogs(filter *ethgo.LogFilter) ([]*ethgo.Log, error) {
	args := m.Called(filter)

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

func TestNewEventTracker(t *testing.T) {
	t.Parallel()

	t.Run("creates a tracker with a valid configuration", func(t *testing.T) {
		t.Parallel()

		config := createTestTrackerConfig(t, 3, 4, nil)

		_, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.NoError(t, err)
	})

	t.Run("creates a default store when store is not provided", func(t *testing.T) {
		t.Parallel()

		_, err := NewEventTracker(createTestTrackerConfig(t, 3, 4, nil), nil)
		require.NoError(t, err)

		// Remove default.db file created during test
		if _, err = os.Stat(defaultStore); err == nil {
			os.RemoveAll(defaultStore)
		}
	})

	t.Run("uses a default logger when logger is not set", func(t *testing.T) {
		t.Parallel()

		config := createTestTrackerConfig(t, 3, 4, nil)
		config.Logger = nil

		_, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.NoError(t, err)
	})

	t.Run("returns error when config is not provided", func(t *testing.T) {
		t.Parallel()

		_, err := NewEventTracker(nil, nil)
		require.ErrorContains(t, err, "invalid configuration")
	})

	t.Run("returns error when event subscriber is not set", func(t *testing.T) {
		t.Parallel()

		_, err := NewEventTracker(createTestTrackerConfigInvalidSub(t, 3, 10),
			store.NewTestTrackerStore(t))
		require.ErrorContains(t, err, "invalid configuration, event subscriber not set")
	})

	t.Run("defaults to the block confirmations strategy", func(t *testing.T) {
		t.Parallel()

		config := createTestTrackerConfig(t, 3, 4, nil)

		_, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.NoError(t, err)
		require.Equal(t, ConfirmationStrategyNumBlockConfirmations, config.ConfirmationStrategy)
	})

	t.Run("creates a tracker with the finalized strategy", func(t *testing.T) {
		t.Parallel()

		config := createTestTrackerConfig(t, 3, 4, nil)
		config.ConfirmationStrategy = ConfirmationStrategyFinalized

		_, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.NoError(t, err)
	})

	t.Run("returns error for an unknown confirmation strategy", func(t *testing.T) {
		t.Parallel()

		config := createTestTrackerConfig(t, 3, 4, nil)
		config.ConfirmationStrategy = "latest"

		_, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.ErrorContains(t, err, "unknown confirmation strategy: latest")
	})

	t.Run("returns error when sync batch size is zero", func(t *testing.T) {
		t.Parallel()

		config := createTestTrackerConfig(t, 3, 0, nil)

		_, err := NewEventTracker(config, store.NewTestTrackerStore(t))
		require.ErrorContains(t, err, "sync batch size must be greater than zero")
	})
}

func TestEventTracker_ProcessAvailableLogs(t *testing.T) {
	t.Parallel()

	provider := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 4, provider)

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(90))

	provider.On("BlockNumber").Return(uint64(105), nil).Once()

	provider.On("GetLogs", mock.MatchedBy(matchesLogRange(91, 94))).Return([]*ethgo.Log{}, nil).Once()
	provider.On("GetLogs", mock.MatchedBy(matchesLogRange(95, 98))).Return([]*ethgo.Log{}, nil).Once()
	provider.On("GetLogs", mock.MatchedBy(matchesLogRange(99, 102))).Return([]*ethgo.Log{}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processAvailableLogs(context.Background()))

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(102), lastProcessedBlock)

	provider.AssertNotCalled(t, "GetBlockByNumber", mock.Anything, mock.Anything)
	provider.AssertExpectations(t)
}

func TestEventTracker_ProcessAvailableLogs_InsufficientChainHeight(t *testing.T) {
	t.Parallel()

	provider := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 4, provider)

	trackerStore := store.NewTestTrackerStore(t)

	provider.On("BlockNumber").Return(uint64(3), nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processAvailableLogs(context.Background()))

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(0), lastProcessedBlock)

	provider.AssertNotCalled(t, "GetLogs", mock.Anything)
	provider.AssertExpectations(t)
}

func TestEventTracker_ProcessAvailableLogs_StartBlockFromGenesis(t *testing.T) {
	t.Parallel()

	provider := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 10, provider)
	config.StartBlockFromGenesis = 50

	trackerStore := store.NewTestTrackerStore(t)

	provider.On("BlockNumber").Return(uint64(58), nil).Once()
	provider.On("GetLogs", mock.MatchedBy(matchesLogRange(51, 55))).
		Return([]*ethgo.Log{}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processAvailableLogs(context.Background()))

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(55), lastProcessedBlock)

	provider.AssertExpectations(t)
}

func TestEventTracker_ProcessAvailableLogs_Finalized(t *testing.T) {
	t.Parallel()

	provider := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 10, provider)
	config.ConfirmationStrategy = ConfirmationStrategyFinalized

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	provider.On("GetBlockByNumber", ethgo.Finalized, false).
		Return(&ethgo.Block{Number: 105}, nil).Once()
	provider.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 105))).
		Return([]*ethgo.Log{}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processAvailableLogs(context.Background()))

	// NumBlockConfirmations is not applied on top of a finalized block
	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(105), lastProcessedBlock)

	provider.AssertNotCalled(t, "BlockNumber")
	provider.AssertExpectations(t)
}

func TestEventTracker_ProcessAvailableLogs_FinalizedNotSupported(t *testing.T) {
	t.Parallel()

	provider := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 10, provider)
	config.ConfirmationStrategy = ConfirmationStrategyFinalized

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	// a node that does not support the tag answers with a null block and no error
	provider.On("GetBlockByNumber", ethgo.Finalized, false).Return(nil, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.ErrorContains(t, eventTracker.processAvailableLogs(context.Background()),
		"block finalized not found on the tracked chain")

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(100), lastProcessedBlock)

	provider.AssertNotCalled(t, "GetLogs", mock.Anything)
	provider.AssertExpectations(t)
}

func TestEventTracker_ProcessAvailableLogs_BlockNumberError(t *testing.T) {
	t.Parallel()

	provider := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 4, provider)

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(90))

	provider.On("BlockNumber").Return(uint64(0), errors.New("rpc down")).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.ErrorContains(t, eventTracker.processAvailableLogs(context.Background()), "rpc down")

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(90), lastProcessedBlock)

	provider.AssertNotCalled(t, "GetLogs", mock.Anything)
	provider.AssertExpectations(t)
}

func TestEventTracker_ProcessLogsRange_EmptyRangeAdvancesCheckpoint(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 20, providerMock)

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 120))).
		Return([]*ethgo.Log{}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processLogsRange(101, 120))

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(120), lastProcessedBlock)

	logs, err := trackerStore.GetAllLogs()
	require.NoError(t, err)
	require.Empty(t, logs)

	providerMock.AssertExpectations(t)
}

func TestEventTracker_ProcessLogsRange_GetLogsErrorDoesNotAdvanceCheckpoint(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 20, providerMock)

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 120))).
		Return(nil, errors.New("rpc down")).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.ErrorContains(t, eventTracker.processLogsRange(101, 120), "rpc down")

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(100), lastProcessedBlock)

	providerMock.AssertExpectations(t)
}

func TestEventTracker_ProcessLogsRange_SubscriberErrorDoesNotAdvanceCheckpoint(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	subscriber := &mockEventSubscriber{addLogErrors: []error{errors.New("subscriber unavailable")}}
	config := createTestTrackerConfig(t, 3, 20, providerMock)
	config.EventSubscriber = subscriber

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	log := store.CreateTestLogForStateSyncEvent(t, 101, 0)
	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 120))).
		Return([]*ethgo.Log{log}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	err = eventTracker.processLogsRange(101, 120)
	require.ErrorContains(t, err, "subscriber unavailable")

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(100), lastProcessedBlock)

	logs, err := trackerStore.GetAllLogs()
	require.NoError(t, err)
	require.Empty(t, logs)

	providerMock.AssertExpectations(t)
}

func TestEventTracker_ProcessLogsRange_RetrySkipsPreviouslyPublishedLogs(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	subscriber := &mockEventSubscriber{
		addLogErrors: []error{nil, errors.New("subscriber unavailable")},
	}
	config := createTestTrackerConfig(t, 3, 20, providerMock)
	config.EventSubscriber = subscriber

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	firstLog := store.CreateTestLogForStateSyncEvent(t, 101, 0)
	firstLog.BlockHash = ethgo.Hash{1}
	firstLog.TransactionHash = ethgo.Hash{2}
	secondLog := store.CreateTestLogForStateSyncEvent(t, 102, 0)
	secondLog.BlockHash = ethgo.Hash{3}
	secondLog.TransactionHash = ethgo.Hash{4}
	logs := []*ethgo.Log{firstLog, secondLog}

	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 120))).
		Return(logs, nil).Twice()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	err = eventTracker.processLogsRange(101, 120)
	require.ErrorContains(t, err, "subscriber unavailable")
	require.Len(t, subscriber.logs, 1)

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(100), lastProcessedBlock)

	require.NoError(t, eventTracker.processLogsRange(101, 120))
	require.Len(t, subscriber.logs, 2)

	lastProcessedBlock, err = trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(120), lastProcessedBlock)

	storedLogs, err := trackerStore.GetAllLogs()
	require.NoError(t, err)
	require.Len(t, storedLogs, 2)

	providerMock.AssertExpectations(t)
}

func TestEventTracker_ProcessLogsRange_DeduplicatesRPCLogs(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	subscriber := new(mockEventSubscriber)
	config := createTestTrackerConfig(t, 3, 20, providerMock)
	config.EventSubscriber = subscriber

	trackerStore := store.NewTestTrackerStore(t)
	firstLog := store.CreateTestLogForStateSyncEvent(t, 101, 0)
	firstLog.BlockHash = ethgo.Hash{1}
	firstLog.TransactionHash = ethgo.Hash{2}
	secondLog := store.CreateTestLogForStateSyncEvent(t, 101, 1)
	secondLog.BlockHash = firstLog.BlockHash
	secondLog.TransactionHash = firstLog.TransactionHash

	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 120))).
		Return([]*ethgo.Log{firstLog, firstLog, secondLog}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processLogsRange(101, 120))
	require.Len(t, subscriber.logs, 2)

	logs, err := trackerStore.GetAllLogs()
	require.NoError(t, err)
	require.Len(t, logs, 2)

	providerMock.AssertExpectations(t)
}

func TestEventTracker_ProcessLogsRange_SkipsPersistedLog(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	subscriber := new(mockEventSubscriber)
	config := createTestTrackerConfig(t, 3, 20, providerMock)
	config.EventSubscriber = subscriber

	trackerStore := store.NewTestTrackerStore(t)
	log := store.CreateTestLogForStateSyncEvent(t, 101, 0)
	require.NoError(t, trackerStore.InsertLogs([]*ethgo.Log{log}))
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 120))).
		Return([]*ethgo.Log{log}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processLogsRange(101, 120))
	require.Empty(t, subscriber.logs)

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(120), lastProcessedBlock)

	providerMock.AssertExpectations(t)
}

// TestEventTracker_ProcessAvailableLogs_ResumesFromOldCheckpoint guards the promise
// that a tracker which was down for a long time resumes from the block right after
// its checkpoint, without any window that would silently skip confirmed blocks.
func TestEventTracker_ProcessAvailableLogs_ResumesFromOldCheckpoint(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 30, providerMock)

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(50))

	providerMock.On("BlockNumber").Return(uint64(105), nil).Once()
	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(51, 80))).
		Return([]*ethgo.Log{}, nil).Once()
	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(81, 102))).
		Return([]*ethgo.Log{}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processAvailableLogs(context.Background()))

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(102), lastProcessedBlock)

	providerMock.AssertExpectations(t)
}

func TestEventTracker_TrackLogs_StopsOnCancelledContext(t *testing.T) {
	t.Parallel()

	providerMock := new(mockProvider)
	config := createTestTrackerConfig(t, 3, 4, providerMock)

	trackerStore := store.NewTestTrackerStore(t)
	require.NoError(t, trackerStore.InsertLastProcessedBlock(100))

	providerMock.On("BlockNumber").Return(uint64(101), nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, eventTracker.trackLogs(ctx), context.Canceled)

	providerMock.AssertExpectations(t)
}

func createTestTrackerConfig(t *testing.T,
	numBlockConfirmations, batchSize uint64,
	providerMock *mockProvider) *EventTrackerConfig {
	t.Helper()

	if providerMock == nil {
		providerMock = new(mockProvider)
	}

	providerMock.On("ChainID").Return(big.NewInt(1), nil).Once()

	return &EventTrackerConfig{
		RPCEndpoint:           "http://some-rpc-url.com",
		NumBlockConfirmations: numBlockConfirmations,
		SyncBatchSize:         batchSize,
		PollInterval:          2 * time.Second,
		Logger:                hclog.NewNullLogger(),
		LogFilter: map[ethgo.Address][]ethgo.Hash{
			ethgo.ZeroAddress: {store.StateSyncEventABI.ID()},
		},
		EventSubscriber: new(mockEventSubscriber),
		Provider:        providerMock,
	}
}

func createTestTrackerConfigInvalidSub(t *testing.T,
	numBlockConfirmations, batchSize uint64) *EventTrackerConfig {
	t.Helper()

	return &EventTrackerConfig{
		RPCEndpoint:           "http://some-rpc-url.com",
		NumBlockConfirmations: numBlockConfirmations,
		SyncBatchSize:         batchSize,
		PollInterval:          2 * time.Second,
		Logger:                hclog.NewNullLogger(),
		LogFilter: map[ethgo.Address][]ethgo.Hash{
			ethgo.ZeroAddress: {store.StateSyncEventABI.ID()},
		},
		EventSubscriber: nil,
		Provider:        new(mockProvider),
	}
}
