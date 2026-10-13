package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/Ethernal-Tech/ethgo"
	"github.com/Ethernal-Tech/ethgo/abi"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

var StateSyncEventABI = abi.MustNewEvent("event StateSynced(uint256 indexed id, " +
	"address indexed sender, address indexed receiver, bytes data)")

func CreateTestLogForStateSyncEvent(t *testing.T, blockNumber, logIndex uint64) *ethgo.Log {
	t.Helper()

	topics := make([]ethgo.Hash, 3)
	topics[0] = StateSyncEventABI.ID()
	topics[1] = ethgo.BytesToHash(ethgo.ZeroAddress.Bytes())
	topics[2] = ethgo.BytesToHash(ethgo.ZeroAddress.Bytes())
	encodedData, err := abi.MustNewType("tuple(string a)").Encode([]string{"data"})
	require.NoError(t, err)

	return &ethgo.Log{
		BlockNumber: blockNumber,
		LogIndex:    logIndex,
		Address:     ethgo.ZeroAddress,
		Topics:      topics,
		Data:        encodedData,
	}
}

// NewTestTrackerStore creates new instance of state used by tests.
func NewTestTrackerStore(tb testing.TB) *BoltDBEventTrackerStore {
	tb.Helper()

	dir := fmt.Sprintf("/tmp/even-tracker-temp_%v", time.Now().UTC().Format(time.RFC3339Nano))
	err := os.Mkdir(dir, 0775)

	if err != nil {
		tb.Fatal(err)
	}

	store, err := NewBoltDBEventTrackerStore(path.Join(dir, "tracker.db"))
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			tb.Fatal(err)
		}
	})

	return store
}

// The names and the encoding below are the on-disk layout of the databases that the
// already running trackers created. They are spelled out literally, instead of being
// taken from the store, so that a rename in the store fails the compatibility tests
// instead of silently orphaning every existing database.
var (
	legacyLastProcessedBlockBucket = []byte("lastProcessedTrackerBucket")
	legacyLastProcessedBlockKey    = []byte("lastProcessedTrackerBlock")
	legacyLogsBucket               = []byte("logs")
)

// LegacyTestLogKey builds a log key the way the already running trackers build it,
// as the block number followed by the log index, both big endian.
func LegacyTestLogKey(blockNumber, logIndex uint64) []byte {
	key := make([]byte, 16)
	binary.BigEndian.PutUint64(key[:8], blockNumber)
	binary.BigEndian.PutUint64(key[8:], logIndex)

	return key
}

// WriteLegacyTestDatabase creates a database at dbPath in the layout of the already
// running trackers. It writes through raw BoltDB, bypassing the store, so that tests
// which read it exercise a database this code has never written to.
func WriteLegacyTestDatabase(
	tb testing.TB, dbPath string, lastProcessedBlock uint64, logs []*ethgo.Log,
) {
	tb.Helper()

	db, err := bolt.Open(dbPath, 0666, nil)
	require.NoError(tb, err)

	require.NoError(tb, db.Update(func(tx *bolt.Tx) error {
		checkpointBucket, err := tx.CreateBucketIfNotExists(legacyLastProcessedBlockBucket)
		if err != nil {
			return err
		}

		checkpoint := make([]byte, 8)
		binary.BigEndian.PutUint64(checkpoint, lastProcessedBlock)

		if err := checkpointBucket.Put(legacyLastProcessedBlockKey, checkpoint); err != nil {
			return err
		}

		logsBucket, err := tx.CreateBucketIfNotExists(legacyLogsBucket)
		if err != nil {
			return err
		}

		for _, log := range logs {
			raw, err := json.Marshal(log)
			if err != nil {
				return err
			}

			if err := logsBucket.Put(LegacyTestLogKey(log.BlockNumber, log.LogIndex), raw); err != nil {
				return err
			}
		}

		return nil
	}))

	require.NoError(tb, db.Close())
}

// NewLegacyTestTrackerStore creates a database in the layout of the already running
// trackers and opens it with the store under test.
func NewLegacyTestTrackerStore(
	tb testing.TB, lastProcessedBlock uint64, logs []*ethgo.Log,
) *BoltDBEventTrackerStore {
	tb.Helper()

	dbPath := path.Join(tb.TempDir(), "tracker.db")

	WriteLegacyTestDatabase(tb, dbPath, lastProcessedBlock, logs)

	store, err := NewBoltDBEventTrackerStore(dbPath)
	require.NoError(tb, err)

	tb.Cleanup(func() {
		require.NoError(tb, store.Close())
	})

	return store
}
