package store

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
)

func TestEventTrackerStore_InsertAndGetLastProcessedBlock(t *testing.T) {
	t.Parallel()

	t.Run("No blocks inserted", func(t *testing.T) {
		t.Parallel()

		store := NewTestTrackerStore(t)

		result, err := store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, uint64(0), result)
	})

	t.Run("Has a block inserted", func(t *testing.T) {
		t.Parallel()

		store := NewTestTrackerStore(t)

		require.NoError(t, store.InsertLastProcessedBlock(10))

		result, err := store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, uint64(10), result)
	})

	t.Run("Insert a bunch of blocks - only the last one should be the result", func(t *testing.T) {
		t.Parallel()

		store := NewTestTrackerStore(t)

		for i := uint64(0); i <= 20; i++ {
			require.NoError(t, store.InsertLastProcessedBlock(i))
		}

		result, err := store.GetLastProcessedBlock()
		require.NoError(t, err)
		require.Equal(t, uint64(20), result)
	})
}

func TestEventTrackerStore_InsertAndGetLogs(t *testing.T) {
	t.Parallel()

	t.Run("No logs inserted", func(t *testing.T) {
		t.Parallel()

		store := NewTestTrackerStore(t)

		log, err := store.GetLog(1, 1)
		require.NoError(t, err)
		require.Nil(t, log)

		logs, err := store.GetLogsByBlockNumber(1)
		require.NoError(t, err)
		require.Nil(t, logs)
	})

	t.Run("Has some logs in store, but no desired log", func(t *testing.T) {
		t.Parallel()

		store := NewTestTrackerStore(t)

		require.NoError(t, store.InsertLogs([]*ethgo.Log{CreateTestLogForStateSyncEvent(t, 1, 0)}))

		log, err := store.GetLog(1, 1)
		require.NoError(t, err)
		require.Nil(t, log)
	})

	t.Run("Has some logs in store, but no desired logs for specific block", func(t *testing.T) {
		t.Parallel()

		store := NewTestTrackerStore(t)

		require.NoError(t, store.InsertLogs([]*ethgo.Log{CreateTestLogForStateSyncEvent(t, 1, 0)}))

		logs, err := store.GetLogsByBlockNumber(2)
		require.NoError(t, err)
		require.Nil(t, logs)
	})

	t.Run("Has bunch of logs", func(t *testing.T) {
		t.Parallel()

		numOfBlocks := 10
		numOfLogsPerBlock := 5

		store := NewTestTrackerStore(t)

		for i := 1; i <= numOfBlocks; i++ {
			blockLogs := make([]*ethgo.Log, numOfLogsPerBlock)
			for j := 0; j < numOfLogsPerBlock; j++ {
				blockLogs[j] = CreateTestLogForStateSyncEvent(t, uint64(i), uint64(j))
			}

			require.NoError(t, store.InsertLogs(blockLogs))
		}

		// check if the num of blocks per each block matches expected values
		for i := 1; i <= numOfBlocks; i++ {
			logs, err := store.GetLogsByBlockNumber(uint64(i))
			require.NoError(t, err)
			require.Len(t, logs, numOfLogsPerBlock)
		}

		// get logs for non existing block
		logs, err := store.GetLogsByBlockNumber(uint64(numOfBlocks + 1))
		require.NoError(t, err)
		require.Nil(t, logs)

		// get specific logs
		for i := 1; i <= numOfBlocks; i++ {
			for j := 0; j < numOfLogsPerBlock; j++ {
				log, err := store.GetLog(uint64(i), uint64(j))
				require.NoError(t, err)
				require.NotNil(t, log)
				require.Equal(t, uint64(i), log.BlockNumber)
				require.Equal(t, uint64(j), log.LogIndex)
			}
		}

		// get some non existing logs
		log, err := store.GetLog(1, uint64(numOfLogsPerBlock+1))
		require.NoError(t, err)
		require.Nil(t, log)

		log, err = store.GetLog(uint64(numOfBlocks+1), uint64(numOfLogsPerBlock+1))
		require.NoError(t, err)
		require.Nil(t, log)
	})
}
