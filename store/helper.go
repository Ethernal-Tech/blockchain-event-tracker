package store

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
)

var StateSyncEventABI = abi.MustNewEvent("event StateSynced(uint256 indexed id, address indexed sender, address indexed receiver, bytes data)")

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
