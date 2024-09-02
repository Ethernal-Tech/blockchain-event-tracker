package tracker

import (
	"testing"

	"github.com/Ethernal-Tech/ethgo"
	"github.com/stretchr/testify/require"
)

func TestTrackerBlockContainer_GetConfirmedBlocks(t *testing.T) {
	t.Parallel()

	t.Run("Number of blocks is greater than numBlockConfirmations", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)
		tbc.blocks = []uint64{1, 2, 3, 4, 5}

		numBlockConfirmations := uint64(2)
		expected := []uint64{1, 2, 3}

		result := tbc.GetConfirmedBlocks(numBlockConfirmations)

		require.Equal(t, expected, result)
	})

	t.Run("Number of blocks is less or equal than numBlockConfirmations", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)
		tbc.blocks = []uint64{1, 2, 3}
		numBlockConfirmations := uint64(3)

		result := tbc.GetConfirmedBlocks(numBlockConfirmations)

		require.Nil(t, result)
	})

	t.Run("numBlockConfirmations is 0", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)
		tbc.blocks = []uint64{1, 2, 3}
		numBlockConfirmations := uint64(0)

		result := tbc.GetConfirmedBlocks(numBlockConfirmations)

		require.Equal(t, tbc.blocks, result)
	})

	t.Run("numBlockConfirmations is 1", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)
		tbc.blocks = []uint64{1, 2, 3}

		numBlockConfirmations := uint64(1)
		expected := []uint64{1, 2}

		result := tbc.GetConfirmedBlocks(numBlockConfirmations)

		require.Equal(t, expected, result)
	})

	t.Run("No blocks cached", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)
		tbc.blocks = []uint64{}

		numBlockConfirmations := uint64(0)

		result := tbc.GetConfirmedBlocks(numBlockConfirmations)

		require.Nil(t, result)
	})
}

func TestTrackerBlockContainer_IsOutOfSync(t *testing.T) {
	t.Parallel()

	t.Run("Block number greater than last cached block and parent hash matches", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		cachedBlock := &ethgo.Block{
			Number:     2,
			Hash:       ethgo.Hash{2},
			ParentHash: ethgo.Hash{1},
		}
		require.NoError(t, tbc.AddBlock(cachedBlock))

		latestBlock := &ethgo.Block{
			Number:     3,
			Hash:       ethgo.Hash{3},
			ParentHash: cachedBlock.Hash,
		}

		require.False(t, tbc.IsOutOfSync(latestBlock))
	})

	t.Run("Latest block number equal to 1 (start of chain)", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		require.False(t, tbc.IsOutOfSync(&ethgo.Block{Number: 1}))
	})

	t.Run("Block number greater than last cached block and parent hash does not match", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		cachedBlock := &ethgo.Block{
			Number:     2,
			Hash:       ethgo.Hash{2},
			ParentHash: ethgo.Hash{1},
		}
		require.NoError(t, tbc.AddBlock(cachedBlock))

		latestBlock := &ethgo.Block{
			Number:     3,
			Hash:       ethgo.Hash{3},
			ParentHash: ethgo.Hash{22}, // some other parent
		}

		require.True(t, tbc.IsOutOfSync(latestBlock))
	})

	t.Run("Block number less or equal to the last cached block", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		cachedBlock := &ethgo.Block{
			Number:     2,
			Hash:       ethgo.Hash{2},
			ParentHash: ethgo.Hash{1},
		}
		require.NoError(t, tbc.AddBlock(cachedBlock))

		require.False(t, tbc.IsOutOfSync(cachedBlock))
	})

	t.Run("Block number greater than the last cached block, and parent does not exist", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		cachedBlock := &ethgo.Block{
			Number:     12,
			Hash:       ethgo.Hash{2},
			ParentHash: ethgo.Hash{1},
		}
		require.NoError(t, tbc.AddBlock(cachedBlock))

		require.False(t, tbc.IsOutOfSync(cachedBlock))
	})
}

func TestTrackerBlockContainer_RemoveBlocks(t *testing.T) {
	t.Parallel()

	t.Run("Remove blocks from 'from' to 'last' index, and update lastProcessedConfirmedBlock", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		// Add some blocks to the container
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 1, Hash: ethgo.Hash{1}, ParentHash: ethgo.Hash{0}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 2, Hash: ethgo.Hash{2}, ParentHash: ethgo.Hash{1}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 3, Hash: ethgo.Hash{3}, ParentHash: ethgo.Hash{2}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 4, Hash: ethgo.Hash{4}, ParentHash: ethgo.Hash{3}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 5, Hash: ethgo.Hash{5}, ParentHash: ethgo.Hash{4}}))

		require.NoError(t, tbc.RemoveBlocks(1, 3))

		// Check if the blocks and lastProcessedConfirmedBlock are updated correctly
		require.Equal(t, []uint64{4, 5}, tbc.blocks)
		require.Equal(t, uint64(3), tbc.lastProcessedConfirmedBlock)
	})

	t.Run("Remove blocks from 'from' to 'last' index, where 'from' is greater than 'last'", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		// Add some blocks to the container
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 1, Hash: ethgo.Hash{1}, ParentHash: ethgo.Hash{0}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 2, Hash: ethgo.Hash{2}, ParentHash: ethgo.Hash{1}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 3, Hash: ethgo.Hash{3}, ParentHash: ethgo.Hash{2}}))

		require.ErrorContains(t, tbc.RemoveBlocks(3, 1), "greater than last block")
	})

	t.Run("Remove blocks from 'from' to 'last' index, where given already removed", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(30)

		// Add some blocks to the container
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 21, Hash: ethgo.Hash{21}, ParentHash: ethgo.Hash{20}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 22, Hash: ethgo.Hash{22}, ParentHash: ethgo.Hash{21}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 23, Hash: ethgo.Hash{23}, ParentHash: ethgo.Hash{22}}))

		require.ErrorContains(t, tbc.RemoveBlocks(18, 19), "are already processed and removed")
	})

	t.Run("Remove blocks from 'from' to 'last' index, where last does not exist in cached blocks", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(10)

		// Add some blocks to the container
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 11, Hash: ethgo.Hash{11}, ParentHash: ethgo.Hash{10}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 12, Hash: ethgo.Hash{12}, ParentHash: ethgo.Hash{11}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 13, Hash: ethgo.Hash{13}, ParentHash: ethgo.Hash{12}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 14, Hash: ethgo.Hash{14}, ParentHash: ethgo.Hash{13}}))

		require.ErrorContains(t, tbc.RemoveBlocks(11, 15), "could not find last block")
	})

	t.Run("Remove all blocks", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		// Add some blocks to the container
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 1, Hash: ethgo.Hash{1}, ParentHash: ethgo.Hash{0}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 2, Hash: ethgo.Hash{2}, ParentHash: ethgo.Hash{1}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 3, Hash: ethgo.Hash{3}, ParentHash: ethgo.Hash{2}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 4, Hash: ethgo.Hash{4}, ParentHash: ethgo.Hash{3}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 5, Hash: ethgo.Hash{5}, ParentHash: ethgo.Hash{4}}))

		require.NoError(t, tbc.RemoveBlocks(1, 5))

		require.Empty(t, tbc.blocks)
		require.Equal(t, uint64(5), tbc.lastProcessedConfirmedBlock)
	})

	t.Run("Remove single block", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(0)

		// Add some blocks to the container
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 1, Hash: ethgo.Hash{1}, ParentHash: ethgo.Hash{0}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 2, Hash: ethgo.Hash{2}, ParentHash: ethgo.Hash{1}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 3, Hash: ethgo.Hash{3}, ParentHash: ethgo.Hash{2}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 4, Hash: ethgo.Hash{4}, ParentHash: ethgo.Hash{3}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 5, Hash: ethgo.Hash{5}, ParentHash: ethgo.Hash{4}}))

		require.NoError(t, tbc.RemoveBlocks(1, 1))

		require.Equal(t, []uint64{2, 3, 4, 5}, tbc.blocks)
		require.Equal(t, uint64(1), tbc.lastProcessedConfirmedBlock)
	})

	t.Run("Try to do non-sequential removal of blocks", func(t *testing.T) {
		t.Parallel()

		tbc := NewTrackerBlockContainer(110)

		// Add some blocks to the container
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 111, Hash: ethgo.Hash{111}, ParentHash: ethgo.Hash{110}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 112, Hash: ethgo.Hash{112}, ParentHash: ethgo.Hash{111}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 113, Hash: ethgo.Hash{113}, ParentHash: ethgo.Hash{112}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 114, Hash: ethgo.Hash{114}, ParentHash: ethgo.Hash{113}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 115, Hash: ethgo.Hash{115}, ParentHash: ethgo.Hash{114}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 116, Hash: ethgo.Hash{116}, ParentHash: ethgo.Hash{115}}))
		require.NoError(t, tbc.AddBlock(&ethgo.Block{Number: 117, Hash: ethgo.Hash{117}, ParentHash: ethgo.Hash{116}}))

		// Try to remove last 2 blocks without first removing first 3
		require.ErrorContains(t, tbc.RemoveBlocks(113, 115), "trying to do non-sequential removal")
	})
}

func TestTrackerBlockContainer_AddBlockAndLastCachedBlock(t *testing.T) {
	t.Parallel()

	numOfBlocks := 30
	tbc := NewTrackerBlockContainer(0)

	for i := uint64(1); i <= uint64(numOfBlocks); i++ {
		require.NoError(t, tbc.AddBlock(&ethgo.Block{
			Number:     i,
			Hash:       ethgo.Hash{byte(i)},
			ParentHash: ethgo.Hash{byte(i - 1)},
		}))
		require.Equal(t, i, tbc.LastCachedBlock())
	}

	require.Len(t, tbc.blocks, numOfBlocks)
	require.Len(t, tbc.numToHashMap, numOfBlocks)

	for i := 0; i < numOfBlocks; i++ {
		require.Equal(t, tbc.blocks[i], uint64(i+1))
	}
}
