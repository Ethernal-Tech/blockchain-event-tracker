package store

import (
	"encoding/binary"
	"encoding/json"
	"path"
	"testing"

	"github.com/Ethernal-Tech/ethgo"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

// TestEventTrackerStore_ReadsLegacyDatabase opens a database written in the layout of
// the already running trackers and checks that every read path returns what was stored.
func TestEventTrackerStore_ReadsLegacyDatabase(t *testing.T) {
	t.Parallel()

	firstLog := CreateTestLogForStateSyncEvent(t, 4321, 0)
	firstLog.BlockHash = ethgo.Hash{1}
	firstLog.TransactionHash = ethgo.Hash{2}
	firstLog.TransactionIndex = 7
	secondLog := CreateTestLogForStateSyncEvent(t, 4321, 1)
	secondLog.BlockHash = ethgo.Hash{1}
	secondLog.TransactionHash = ethgo.Hash{3}

	store := NewLegacyTestTrackerStore(t, 4321, []*ethgo.Log{firstLog, secondLog})

	lastProcessedBlock, err := store.GetLastProcessedBlock()
	require.NoError(t, err)
	require.Equal(t, uint64(4321), lastProcessedBlock)

	// the fields below identify a log, so they have to survive the round trip,
	// otherwise the tracker can not recognize an already processed log
	storedLog, err := store.GetLog(4321, 0)
	require.NoError(t, err)
	require.NotNil(t, storedLog)
	require.Equal(t, firstLog.BlockNumber, storedLog.BlockNumber)
	require.Equal(t, firstLog.LogIndex, storedLog.LogIndex)
	require.Equal(t, firstLog.BlockHash, storedLog.BlockHash)
	require.Equal(t, firstLog.TransactionHash, storedLog.TransactionHash)
	require.Equal(t, firstLog.TransactionIndex, storedLog.TransactionIndex)
	require.Equal(t, firstLog.Address, storedLog.Address)
	require.Equal(t, firstLog.Topics, storedLog.Topics)
	require.Equal(t, firstLog.Data, storedLog.Data)

	logsByBlockNumber, err := store.GetLogsByBlockNumber(4321)
	require.NoError(t, err)
	require.Len(t, logsByBlockNumber, 2)

	allLogs, err := store.GetAllLogs()
	require.NoError(t, err)
	require.Len(t, allLogs, 2)

	missingLog, err := store.GetLog(4322, 0)
	require.NoError(t, err)
	require.Nil(t, missingLog)
}

// TestEventTrackerStore_WritesLegacyLayout checks the other direction, that what the
// store writes lands under the bucket names and the keys that the already running
// trackers use. Together with TestEventTrackerStore_ReadsLegacyDatabase this keeps the
// database readable by both the old and the new tracker, and by apex-bridge, which
// reads the same file.
func TestEventTrackerStore_WritesLegacyLayout(t *testing.T) {
	t.Parallel()

	dbPath := path.Join(t.TempDir(), "tracker.db")

	store, err := NewBoltDBEventTrackerStore(dbPath)
	require.NoError(t, err)

	log := CreateTestLogForStateSyncEvent(t, 900, 2)
	log.BlockHash = ethgo.Hash{9}
	log.TransactionHash = ethgo.Hash{8}

	require.NoError(t, store.InsertLogsAndLastProcessedBlock([]*ethgo.Log{log}, 900))
	require.NoError(t, store.Close())

	db, err := bolt.Open(dbPath, 0666, nil)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, db.Close())
	}()

	require.NoError(t, db.View(func(tx *bolt.Tx) error {
		checkpointBucket := tx.Bucket(legacyLastProcessedBlockBucket)
		require.NotNil(t, checkpointBucket)

		checkpoint := checkpointBucket.Get(legacyLastProcessedBlockKey)
		require.Len(t, checkpoint, 8)
		require.Equal(t, uint64(900), binary.BigEndian.Uint64(checkpoint))

		logsBucket := tx.Bucket(legacyLogsBucket)
		require.NotNil(t, logsBucket)

		raw := logsBucket.Get(LegacyTestLogKey(900, 2))
		require.NotNil(t, raw)

		var storedLog ethgo.Log
		require.NoError(t, json.Unmarshal(raw, &storedLog))
		require.Equal(t, log.BlockNumber, storedLog.BlockNumber)
		require.Equal(t, log.LogIndex, storedLog.LogIndex)
		require.Equal(t, log.BlockHash, storedLog.BlockHash)
		require.Equal(t, log.TransactionHash, storedLog.TransactionHash)

		return nil
	}))
}
