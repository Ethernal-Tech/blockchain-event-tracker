package tracker

import (
	"context"
	"testing"

	"github.com/Ethernal-Tech/blockchain-event-tracker/store"
	"github.com/Ethernal-Tech/ethgo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestEventTracker_ResumesFromLegacyDatabase starts the tracker on a database left
// behind by an already running tracker and checks the two things that matter when the
// new binary is deployed over it: it continues from the stored checkpoint, and it does
// not hand an already stored log to the subscriber a second time.
//
// The database also holds a log from a block above the checkpoint. The old tracker
// stored logs and the checkpoint in two separate transactions, so a crash between them
// left exactly this state behind, and the new tracker has to tolerate it.
func TestEventTracker_ResumesFromLegacyDatabase(t *testing.T) {
	t.Parallel()

	alreadyStoredLog := store.CreateTestLogForStateSyncEvent(t, 101, 0)
	alreadyStoredLog.BlockHash = ethgo.Hash{1}
	alreadyStoredLog.TransactionHash = ethgo.Hash{2}

	newLog := store.CreateTestLogForStateSyncEvent(t, 118, 0)
	newLog.BlockHash = ethgo.Hash{3}
	newLog.TransactionHash = ethgo.Hash{4}

	trackerStore := store.NewLegacyTestTrackerStore(t, 100, []*ethgo.Log{alreadyStoredLog})

	providerMock := new(mockProvider)
	subscriber := new(mockEventSubscriber)
	config := createTestTrackerConfig(t, 3, 20, providerMock)
	config.EventSubscriber = subscriber

	providerMock.On("BlockNumber").Return(uint64(125), nil).Once()
	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(101, 120))).
		Return([]*ethgo.Log{alreadyStoredLog, newLog}, nil).Once()
	providerMock.On("GetLogs", mock.MatchedBy(matchesLogRange(121, 122))).
		Return([]*ethgo.Log{}, nil).Once()

	eventTracker, err := NewEventTracker(config, trackerStore)
	require.NoError(t, err)

	require.NoError(t, eventTracker.processAvailableLogs(context.Background()))

	require.Len(t, subscriber.logs, 1)
	require.Equal(t, newLog.BlockNumber, subscriber.logs[0].BlockNumber)

	lastProcessedBlock, err := trackerStore.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(122), lastProcessedBlock)

	storedLogs, err := trackerStore.GetAllLogs()
	require.NoError(t, err)
	require.Len(t, storedLogs, 2)

	providerMock.AssertExpectations(t)
}
