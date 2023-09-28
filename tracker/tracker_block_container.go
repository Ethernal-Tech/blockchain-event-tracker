package tracker

import (
	"fmt"
	"sync"

	"github.com/umbracle/ethgo"
)

// TrackerBlockContainer is a struct used to cache and manage tracked blocks from tracked chain.
// It keeps a map of block numbers to hashes, a slice of block numbers to process,
// and the last processed confirmed block number.
// It also uses a mutex to handle concurrent access to the struct
type TrackerBlockContainer struct {
	numToHashMap                map[uint64]ethgo.Hash
	blocks                      []uint64
	lastProcessedConfirmedBlock uint64
	mux                         sync.RWMutex
}

// NewTrackerBlockContainer is a constructor function that creates a
// new instance of the TrackerBlockContainer struct.
//
// Example Usage:
//
//	t := NewTrackerBlockContainer(1)
//
// Inputs:
//   - lastProcessed (uint64): The last processed block number.
//
// Outputs:
//   - A new instance of the TrackerBlockContainer struct with the lastProcessedConfirmedBlock
//     field set to the input lastProcessed block number and an empty numToHashMap map.
func NewTrackerBlockContainer(lastProcessed uint64) *TrackerBlockContainer {
	return &TrackerBlockContainer{
		lastProcessedConfirmedBlock: lastProcessed,
		numToHashMap:                make(map[uint64]ethgo.Hash),
	}
}

// AcquireWriteLock acquires the write lock on the TrackerBlockContainer
func (t *TrackerBlockContainer) AcquireWriteLock() {
	t.mux.Lock()
}

// ReleaseWriteLock releases the write lock on the TrackerBlockContainer
func (t *TrackerBlockContainer) ReleaseWriteLock() {
	t.mux.Unlock()
}

// LastProcessedBlockLocked returns number of last processed block for logs
// Function acquires the read lock before accessing the lastProcessedConfirmedBlock field
func (t *TrackerBlockContainer) LastProcessedBlock() uint64 {
	t.mux.RLock()
	defer t.mux.RUnlock()

	return t.LastProcessedBlockLocked()
}

// LastProcessedBlockLocked returns number of last processed block for logs
// Function assumes that the read or write lock is already acquired before accessing
// the lastProcessedConfirmedBlock field
func (t *TrackerBlockContainer) LastProcessedBlockLocked() uint64 {
	return t.lastProcessedConfirmedBlock
}

// UpdateLastProcessedBlockLocked updates the number of last processed block for logs
// Function assumes that the write lock is already acquired before accessing
// the lastProcessedConfirmedBlock field
func (t *TrackerBlockContainer) UpdateLastProcessedBlockLocked(lastProcessed uint64) {
	t.lastProcessedConfirmedBlock = lastProcessed
}

// LastCachedBlock returns the block number of the last cached block for processing
//
// Example Usage:
//
//	t := NewTrackerBlockContainer(1)
//	t.AddBlock(&ethgo.Block{Number: 1, Hash: "hash1"})
//	t.AddBlock(&ethgo.Block{Number: 2, Hash: "hash2"})
//	t.AddBlock(&ethgo.Block{Number: 3, Hash: "hash3"})
//
//	lastCachedBlock := t.LastCachedBlock()
//	fmt.Println(lastCachedBlock) // Output: 3
//
// Outputs:
//
//	The output is a uint64 value representing the block number of the last cached block.
func (t *TrackerBlockContainer) LastCachedBlock() uint64 {
	if len(t.blocks) > 0 {
		return t.blocks[len(t.blocks)-1]
	}

	return 0
}

// AddBlock adds a new block to the tracker by storing its number and hash in the numToHashMap map
// and appending the block number to the blocks slice.
//
// Inputs:
//   - block (ethgo.Block): The block to be added to the tracker cache for later processing,
//     once it hits confirmation number.
func (t *TrackerBlockContainer) AddBlock(block *ethgo.Block) error {
	if hash, exists := t.numToHashMap[block.Number-1]; len(t.blocks) > 0 && (!exists || block.ParentHash != hash) {
		return fmt.Errorf("no parent for block %d, or a reorg happened", block.Number)
	}

	t.numToHashMap[block.Number] = block.Hash
	t.blocks = append(t.blocks, block.Number)

	return nil
}

// RemoveBlocks removes processed blocks from cached maps,
// and updates the lastProcessedConfirmedBlock variable, to the last processed block.
//
// Inputs:
// - from (uint64): The starting block number to remove.
// - last (uint64): The ending block number to remove.
//
// Returns:
//   - nil if removal is successful
//   - An error if from block is greater than the last, if given range of blocks was already processed and removed,
//     if the last block could not be found in cached blocks, or if we are trying to do a non sequential removal
func (t *TrackerBlockContainer) RemoveBlocks(from, last uint64) error {
	if from > last {
		return fmt.Errorf("from block: %d, greater than last block: %d", from, last)
	}

	if last < t.lastProcessedConfirmedBlock {
		return fmt.Errorf("blocks until block: %d are already processed and removed", last)
	}

	lastIndex := t.indexOf(last)
	if lastIndex == -1 {
		return fmt.Errorf("could not find last block: %d in cached blocks", last)
	}

	removedBlocks := t.blocks[:lastIndex+1]
	remainingBlocks := t.blocks[lastIndex+1:]

	if removedBlocks[0] != from {
		return fmt.Errorf("trying to do non-sequential removal of blocks. from: %d, last: %d", from, last)
	}

	for i := from; i <= last; i++ {
		delete(t.numToHashMap, i)
	}

	t.blocks = remainingBlocks
	t.lastProcessedConfirmedBlock = last

	return nil
}

// CleanState resets the state of the TrackerBlockContainer
// by clearing the numToHashMap map and setting the blocks slice to an empty slice
// Called when a reorg happened or we are completely out of sync
func (t *TrackerBlockContainer) CleanState() {
	t.numToHashMap = make(map[uint64]ethgo.Hash)
	t.blocks = make([]uint64, 0)
}

// IsOutOfSync checks if tracker is out of sync with the tracked chain.
// Tracker is out of sync with the tracked chain if these conditions are met:
//   - latest block from chain has higher number than the last tracked block
//   - its parent doesn't exist in numToHash map
//   - its parent hash doesn't match with the hash of the given parent block we tracked,
//     meaning, a reorg happened
//
// Inputs:
// - block (ethgo.Block): The latest block of the tracked chain.
//
// Outputs:
// - outOfSync (bool): A boolean value indicating if the tracker is out of sync (true) or not (false).
func (t *TrackerBlockContainer) IsOutOfSync(block *ethgo.Block) bool {
	t.mux.RLock()
	defer t.mux.RUnlock()

	if block.Number == 1 {
		// if the chain we are tracking just started
		return false
	}

	parentHash, parentExists := t.numToHashMap[block.Number-1]

	return block.Number > t.LastCachedBlock() && (!parentExists || parentHash != block.ParentHash)
}

// GetConfirmedBlocks returns a slice of uint64 representing the block numbers of confirmed blocks.
//
// Example Usage:
//
//	t := NewTrackerBlockContainer(2)
//	t.AddBlock(&ethgo.Block{Number: 1, Hash: "hash1"})
//	t.AddBlock(&ethgo.Block{Number: 2, Hash: "hash2"})
//	t.AddBlock(&ethgo.Block{Number: 3, Hash: "hash3"})
//
//	confirmedBlocks := t.GetConfirmedBlocks(2)
//	fmt.Println(confirmedBlocks) // Output: [1]
//
// Inputs:
//   - numBlockConfirmations (uint64): The number of block confirmations to consider.
//
// Flow:
//  1. Convert numBlockConfirmations to an integer numBlockConfirmationsInt.
//  2. Check if the length of t.blocks (slice of block numbers) is greater than numBlockConfirmationsInt.
//  3. If it is, return a sub-slice of t.blocks from the beginning to the length of
//     t.blocks minus numBlockConfirmationsInt.
//  4. If it is not, return nil.
//
// Outputs:
//   - A slice of uint64 representing the block numbers of confirmed blocks.
func (t *TrackerBlockContainer) GetConfirmedBlocks(numBlockConfirmations uint64) []uint64 {
	numBlockConfirmationsInt := int(numBlockConfirmations)
	if len(t.blocks) > numBlockConfirmationsInt {
		return t.blocks[:len(t.blocks)-numBlockConfirmationsInt]
	}

	return nil
}

// indexOf returns the index of a given block number in the blocks slice.
// If the block number is not found, it returns -1
func (t *TrackerBlockContainer) indexOf(block uint64) int {
	index := -1

	for i, b := range t.blocks {
		if b == block {
			index = i

			break
		}
	}

	return index
}
