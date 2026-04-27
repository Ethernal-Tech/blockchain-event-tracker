package tracker

import (
	"context"
	"time"

	"github.com/Ethernal-Tech/ethgo"
)

// JSONBlockTracker implements the BlockTracker interface using
// the http jsonrpc endpoint
type JSONBlockTracker struct {
	pullInterval              time.Duration
	provider                  BlockProvider
	latestBlockNumberStrategy ethgo.BlockNumber
}

// NewJSONBlockTracker creates a new json block tracker
func NewJSONBlockTracker(
	provider BlockProvider, pullInterval time.Duration, latestBlockNumberStrategy ethgo.BlockNumber,
) *JSONBlockTracker {
	return &JSONBlockTracker{
		provider:                  provider,
		pullInterval:              pullInterval,
		latestBlockNumberStrategy: latestBlockNumberStrategy,
	}
}

// Track implements the BlockTracker interface.
// This can take a long time so should be run concurrently.
func (k *JSONBlockTracker) Track(ctx context.Context, handle func(block *ethgo.Block) error) error {
	var lastBlock *ethgo.Block

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-time.After(k.pullInterval):
			block, err := k.provider.GetBlockByNumber(k.latestBlockNumberStrategy, false)
			if err != nil {
				return err
			}

			if lastBlock != nil && lastBlock.Hash == block.Hash {
				continue
			}

			if err := handle(block); err != nil {
				return err
			}

			lastBlock = block
		}
	}
}
