package store

import (
	"bytes"
	"encoding/json"

	"github.com/Ethernal-Tech/blockchain-event-tracker/common"
	"github.com/Ethernal-Tech/ethgo"
	bolt "go.etcd.io/bbolt"
)

var (
	petLastProcessedBlockKey    = []byte("lastProcessedTrackerBlock")
	petLastProcessedBlockBucket = []byte("lastProcessedTrackerBucket")
	petLogsBucket               = []byte("logs")
)

// EventTrackerStore is an interface that defines the behavior of an event tracker store
type EventTrackerStore interface {
	GetLastProcessedBlock() (uint64, error)
	InsertLastProcessedBlock(blockNumber uint64) error
	InsertLogs(logs []*ethgo.Log) error
	GetLogsByBlockNumber(blockNumber uint64) ([]*ethgo.Log, error)
	GetLog(blockNumber, logIndex uint64) (*ethgo.Log, error)
	GetAllLogs() ([]*ethgo.Log, error)
}

var _ EventTrackerStore = (*BoltDBEventTrackerStore)(nil)

// BoltDBEventTrackerStore represents a store for event tracker events
type BoltDBEventTrackerStore struct {
	db *bolt.DB
}

// NewBoltDBEventTrackerStore is a constructor function that creates
// a new instance of the BoltDBEventTrackerStore struct.
//
// Example Usage:
//
//	t := NewBoltDBEventTrackerStore(/edge/polybft/consensus/deposit.db)
//
// Inputs:
//   - dbPath (string): Full path to the event tracker store db.
//
// Outputs:
//   - A new instance of the BoltDBEventTrackerStore struct with a connection to the event tracker store db.
func NewBoltDBEventTrackerStore(dbPath string) (*BoltDBEventTrackerStore, error) {
	db, err := bolt.Open(dbPath, 0666, nil)
	if err != nil {
		return nil, err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(petLastProcessedBlockBucket)
		if err != nil {
			return err
		}

		_, err = tx.CreateBucketIfNotExists(petLogsBucket)

		return err
	})
	if err != nil {
		return nil, err
	}

	return &BoltDBEventTrackerStore{db: db}, nil
}

// GetLastProcessedBlock retrieves the last processed block number from a BoltDB database.
//
// Example Usage:
//
//	store := NewBoltDBEventTrackerStore(db)
//	blockNumber, err := store.GetLastProcessedBlock()
//	if err != nil {
//	  fmt.Println("Error:", err)
//	} else {
//	  fmt.Println("Last processed block number:", blockNumber)
//	}
//
// Outputs:
//
//	blockNumber: The last processed block number retrieved from the database.
//	err: Any error that occurred during the database operation.
func (p *BoltDBEventTrackerStore) GetLastProcessedBlock() (uint64, error) {
	var blockNumber uint64

	err := p.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket(petLastProcessedBlockBucket).Get(petLastProcessedBlockKey)
		if value != nil {
			blockNumber = common.EncodeBytesToUint64(value)
		}

		return nil
	})

	return blockNumber, err
}

// InsertLastProcessedBlock inserts the last processed block number into a BoltDB bucket.
//
// Inputs:
// - lastProcessedBlockNumber (uint64): The block number to be inserted into the bucket.
//
// Outputs:
// - error: An error indicating if there was a problem with the transaction or the insertion.
func (p *BoltDBEventTrackerStore) InsertLastProcessedBlock(lastProcessedBlockNumber uint64) error {
	return p.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(petLastProcessedBlockBucket).Put(
			petLastProcessedBlockKey, common.EncodeUint64ToBytes(lastProcessedBlockNumber))
	})
}

// InsertLogs inserts logs into a BoltDB database, where logs are stored by a composite key:
// - {log.BlockNumber,log.LogIndex}
//
// Example Usage:
//
//	store := &BoltDBEventTrackerStore{db: boltDB}
//	logs := []*ethgo.Log{log1, log2, log3}
//	err := store.InsertLogs(logs)
//	if err != nil {
//	  fmt.Println("Error inserting logs:", err)
//	}
//
// Inputs:
//   - logs: A slice of ethgo.Log structs representing the logs to be inserted into the database.
//
// Outputs:
//   - error: If an error occurs during the insertion process, it is returned. Otherwise, nil is returned.
func (p *BoltDBEventTrackerStore) InsertLogs(logs []*ethgo.Log) error {
	return p.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(petLogsBucket)
		for _, log := range logs {
			raw, err := json.Marshal(log)
			if err != nil {
				return err
			}

			logKey := bytes.Join([][]byte{
				common.EncodeUint64ToBytes(log.BlockNumber),
				common.EncodeUint64ToBytes(log.LogIndex)}, nil)
			if err := bucket.Put(logKey, raw); err != nil {
				return err
			}
		}

		return nil
	})
}

// GetLogsByBlockNumber retrieves all logs that happened in given block from a BoltDB database.
//
// Example Usage:
//
//		store := &BoltDBEventTrackerStore{db: boltDB}
//	 	block := uint64(10)
//		logs, err := store.GetLogsByBlockNumber(block)
//		if err != nil {
//		  fmt.Println("Error getting logs for block: %d. Err: %w", block, err)
//		}
//
// Inputs:
// - blockNumber (uint64): The block number for which the logs need to be retrieved.
//
// Outputs:
// - logs ([]*ethgo.Log): The logs retrieved from the database for the given block number.
// - err (error): Any error that occurred during the transaction or unmarshaling process.
func (p *BoltDBEventTrackerStore) GetLogsByBlockNumber(blockNumber uint64) ([]*ethgo.Log, error) {
	var logs []*ethgo.Log

	err := p.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(petLogsBucket).Cursor()
		prefix := common.EncodeUint64ToBytes(blockNumber)

		for k, v := c.Seek(prefix); bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var log *ethgo.Log
			if err := json.Unmarshal(v, &log); err != nil {
				return err
			}

			logs = append(logs, log)
		}

		return nil
	})

	return logs, err
}

// GetLog retrieves a log from the BoltDB database based on the given block number and log index.
//
// Example Usage:
//
//		store := &BoltDBEventTrackerStore{db: boltDB}
//	 	block := uint64(10)
//		logIndex := uint64(1)
//		log, err := store.GetLog(block, logIndex)
//		if err != nil {
//		  fmt.Println("Error getting log of index: %d for block: %d. Err: %w", logIndex, block, err)
//		}
//
// Inputs:
//   - blockNumber (uint64): The block number of the desired log.
//   - logIndex (uint64): The index of the desired log within the block.
//
// Outputs:
//   - log (*ethgo.Log): The retrieved log from the BoltDB database. If the log does not exist, it will be nil.
//   - err (error): Any error that occurred during the database operation. If no error occurred, it will be nil.
func (p *BoltDBEventTrackerStore) GetLog(blockNumber, logIndex uint64) (*ethgo.Log, error) {
	var log *ethgo.Log

	err := p.db.View(func(tx *bolt.Tx) error {
		logKey := bytes.Join([][]byte{
			common.EncodeUint64ToBytes(blockNumber),
			common.EncodeUint64ToBytes(logIndex)}, nil)

		val := tx.Bucket(petLogsBucket).Get(logKey)
		if val == nil {
			return nil
		}

		return json.Unmarshal(val, &log)
	})

	return log, err
}

// GetAllLogs retrieves all logs from the logs bucket in the BoltDB database and
// returns them as a slice of ethgo.Log structs.
//
// Example Usage:
// store := NewBoltDBEventTrackerStore("path/to/db")
// logs, err := store.GetAllLogs()
//
//	if err != nil {
//	    fmt.Println("Error:", err)
//	    return
//	}
//
//	for _, log := range logs {
//	    fmt.Println(log)
//	}
//
// Outputs:
// The code snippet returns a slice of ethgo.Log structs (logs) and an error (err).
// The logs slice contains all the logs stored in the logs bucket in the BoltDB database.
// The error will be non-nil if there was an issue with the read transaction or unmarshaling the log structs.
func (p *BoltDBEventTrackerStore) GetAllLogs() ([]*ethgo.Log, error) {
	var logs []*ethgo.Log

	err := p.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(petLogsBucket).ForEach(func(k, v []byte) error {
			var log *ethgo.Log
			if err := json.Unmarshal(v, &log); err != nil {
				return err
			}

			logs = append(logs, log)

			return nil
		})
	})

	return logs, err
}
