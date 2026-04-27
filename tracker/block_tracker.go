package tracker

import (
	"context"
	"time"

	"github.com/Ethernal-Tech/ethgo"
)

// JSONBlockTracker implements the BlockTracker interface using
// the http jsonrpc endpoint
type JSONBlockTracker struct {
	pollInterval              time.Duration
	provider                  BlockProvider
	latestBlockNumberStrategy ethgo.BlockNumber
}

// NewJSONBlockTracker creates a new json block tracker
func NewJSONBlockTracker(
	provider BlockProvider, pollInterval time.Duration, latestBlockNumberStrategy ethgo.BlockNumber,
) *JSONBlockTracker {
	return &JSONBlockTracker{
		provider:                  provider,
		pollInterval:              pollInterval,
		latestBlockNumberStrategy: latestBlockNumberStrategy,
	}
}

// Track implements the BlockTracker interface.
// This can take a long time so should be run concurrently.
func (k *JSONBlockTracker) Track(ctx context.Context, handler func(block *ethgo.Block) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-time.After(k.pollInterval):
			block, err := k.provider.GetBlockByNumber(k.latestBlockNumberStrategy, false)
			if err != nil {
				return err
			}

			// no need for `lastBlock != nil && lastBlock.Hash == block.Hash` check
			// because handler will decide if the block is new or not,
			// and if it is not new, it can just return nil without doing anything
			if block == nil {
				continue
			}

			if err := handler(block); err != nil {
				return err
			}
		}
	}
}
