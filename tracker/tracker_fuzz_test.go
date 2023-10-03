package tracker

import (
	"encoding/json"
	"testing"

	"github.com/Ethernal-Tech/blockchain-event-tracker/store"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
)

type getNewStateF struct {
	Address                ethgo.Address
	Number                 uint64
	LastProcessed          uint64
	BatchSize              uint64
	NumBlockConfirmations  uint64
	NumOfBlocksToReconcile uint64
}

func FuzzGetNewState(f *testing.F) {
	seeds := []getNewStateF{
		{
			Address:                ethgo.BytesToAddress([]byte{1}),
			Number:                 25,
			LastProcessed:          9,
			BatchSize:              5,
			NumBlockConfirmations:  3,
			NumOfBlocksToReconcile: 1000,
		},
		{
			Address:                ethgo.BytesToAddress([]byte{1}),
			Number:                 30,
			LastProcessed:          29,
			BatchSize:              5,
			NumBlockConfirmations:  3,
			NumOfBlocksToReconcile: 1000,
		},
		{
			Address:                ethgo.BytesToAddress([]byte{2}),
			Number:                 100,
			LastProcessed:          10,
			BatchSize:              10,
			NumBlockConfirmations:  3,
			NumOfBlocksToReconcile: 15,
		},
	}

	for _, seed := range seeds {
		data, err := json.Marshal(seed)
		if err != nil {
			return
		}

		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		var data getNewStateF
		if err := json.Unmarshal(input, &data); err != nil {
			t.Skip(err)
		}

		providerMock := new(mockProvider)
		for blockNum := data.LastProcessed + 1; blockNum <= data.Number; blockNum++ {
			providerMock.On("GetBlockByNumber", ethgo.BlockNumber(blockNum), false).Return(
				&ethgo.Block{Number: blockNum}, nil).Once()
		}

		logs := []*ethgo.Log{
			store.CreateTestLogForStateSyncEvent(t, 1, 1),
			store.CreateTestLogForStateSyncEvent(t, 1, 11),
			store.CreateTestLogForStateSyncEvent(t, 2, 3),
		}
		providerMock.On("GetLogs", mock.Anything).Return(logs, nil)

		testConfig := createTestTrackerConfig(t, data.NumBlockConfirmations, data.BatchSize, data.NumOfBlocksToReconcile)
		testConfig.BlockProvider = providerMock

		eventTracker := &EventTracker{
			config:         testConfig,
			blockContainer: NewTrackerBlockContainer(data.LastProcessed),
		}

		require.NoError(t, eventTracker.getNewState(&ethgo.Block{Number: data.Number}))
	})
}
