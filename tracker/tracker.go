package tracker

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Ethernal-Tech/blockchain-event-tracker/common"
	eventStore "github.com/Ethernal-Tech/blockchain-event-tracker/store"
	"github.com/Ethernal-Tech/ethgo"
	"github.com/Ethernal-Tech/ethgo/blocktracker"
	"github.com/Ethernal-Tech/ethgo/jsonrpc"
	hcf "github.com/hashicorp/go-hclog"
)

// MaxBlockGapForLatestSync defines the maximum allowed block gap
// for starting synchronization from the latest block.
// If the gap between startBlock and latestBlock exceeds this value,
// synchronization will start from the first block instead.
const maxBlockGapForLatestSync = 16

// EventSubscriber is an interface that defines methods for handling tracked logs (events) from a blockchain
type EventSubscriber interface {
	AddLog(chainID *big.Int, log *ethgo.Log) error
}

// BlockProvider is an interface that defines methods for retrieving blocks and logs from a blockchain
type BlockProvider interface {
	GetBlockByHash(hash ethgo.Hash, full bool) (*ethgo.Block, error)
	GetBlockByNumber(i ethgo.BlockNumber, full bool) (*ethgo.Block, error)
	GetLogs(filter *ethgo.LogFilter) ([]*ethgo.Log, error)
	ChainID() (*big.Int, error)
}

// EventTrackerConfig is a struct that holds configuration of a EventTracker
type EventTrackerConfig struct {
	// RPCEndpoint is the full json rpc url on some node on a tracked chain
	RPCEndpoint string `json:"rpcEndpoint"`

	// NumBlockConfirmations defines how many blocks must pass from a certain block,
	// to consider that block as final on the tracked chain.
	// This is very important for reorgs, and events from the given block will only be
	// processed if it hits this confirmation mark.
	// (e.g., NumBlockConfirmations = 3, and if the last tracked block is 10,
	// events from block 10, will only be processed when we get block 13 from the tracked chain)
	NumBlockConfirmations uint64 `json:"numBlockConfirmations"`

	// SyncBatchSize defines a batch size of blocks that will be gotten from tracked chain,
	// when tracker is out of sync and needs to sync a number of blocks.
	// (e.g., SyncBatchSize = 10, trackers last processed block is 10, latest block on tracked chain is 100,
	// it will get blocks 11-20, get logs from confirmed blocks of given batch, remove processed confirm logs
	// from memory, and continue to the next batch)
	SyncBatchSize uint64 `json:"syncBatchSize"`

	// NumOfBlocksToReconcile defines how many blocks we will sync up from the latest block on tracked chain.
	// If a node that has tracker, was offline for days, months, a year, it will miss a lot of blocks.
	// In the meantime, we expect the rest of nodes to have collected the desired events and did their
	// logic with them, continuing consensus and relayer stuff.
	// In order to not waste too much unnecessary time in syncing all those blocks, with NumOfBlocksToReconcile,
	// we tell the tracker to sync only latestBlock.Number - NumOfBlocksToReconcile number of blocks.
	NumOfBlocksToReconcile uint64 `json:"numOfBlocksToReconcile"`

	// PollInterval defines a time interval in which tracker polls json rpc node
	// for latest block on the tracked chain.
	PollInterval time.Duration `json:"pollInterval"`

	// LogFilter defines which events are tracked and from which contracts on the tracked chain
	LogFilter map[ethgo.Address][]ethgo.Hash `json:"logFilter"`

	// StartBlockFromConfig defines the block from which the tracker will start tracking events.
	StartBlockFromGenesis uint64 `json:"startBlockFromGenesis"`

	// Logger is the logger instance for event tracker
	Logger hcf.Logger `json:"-"`

	// BlockProvider is the implementation of a provider that returns blocks and logs from tracked chain
	BlockProvider BlockProvider `json:"-"`

	// Client is the jsonrpc client
	RPCClient *jsonrpc.Client `json:"-"`

	// EventSubscriber is the subscriber that requires events tracked by the event tracker
	EventSubscriber EventSubscriber `json:"-"`
}

var defaultStore = "./eventStore.db"

// EventTracker represents a tracker for events on desired contracts on some chain
type EventTracker struct {
	config *EventTrackerConfig

	once sync.Once

	blockTracker   blocktracker.BlockTrackerInterface
	blockContainer *TrackerBlockContainer

	store eventStore.EventTrackerStore

	chainID *big.Int
}

// NewEventTracker is a constructor function that creates a new instance of the EventTracker struct.
//
// Example Usage:
//
//	config := &EventTracker{
//		RpcEndpoint:           "http://some-json-rpc-url.com",
//		StartBlockFromConfig:  100_000,
//		NumBlockConfirmations: 10,
//		SyncBatchSize:         20,
//		NumOfBlocksToReconcile:10_000,
//		PollInterval:          2 * time.Second,
//		Logger:                logger,
//		Store:                 store,
//		EventSubscriber:       subscriber,
//		Provider:              provider,
//		LogFilter: TrackerLogFilter{
//			Addresses: []ethgo.Address{addressOfSomeContract},
//			IDs:       []ethgo.Hash{idHashOfSomeEvent},
//		},
//	}
//		t := NewEventTracker(config)
//
// Inputs:
//   - config (TrackerConfig): configuration of EventTracker.
//   - store: implementation of EventTrackerStore interface
//
// Outputs:
//   - A new instance of the EventTracker struct.
func NewEventTracker(config *EventTrackerConfig, store eventStore.EventTrackerStore) (*EventTracker, error) {
	if config == nil {
		return nil, fmt.Errorf("invalid configuration. Failed to init Event Tracker")
	}

	if config.Logger == nil {
		config.Logger = hcf.NewNullLogger().Named("event-tracker")
	}

	if config.EventSubscriber == nil {
		return nil, fmt.Errorf("invalid configuration, event subscriber not set. Failed to init Event Tracker")
	}

	if store == nil {
		var err error

		store, err = eventStore.NewBoltDBEventTrackerStore(defaultStore)
		if err != nil {
			return nil, err
		}
	}

	// if block provider is not provided externally,
	// we can start the ethgo one
	if err := setupBlockProvider(config, false); err != nil {
		return nil, err
	}

	chainID, err := config.BlockProvider.ChainID()
	if err != nil {
		return nil, err
	}

	lastProcessedBlock, err := store.GetLastProcessedBlock()
	if err != nil {
		return nil, err
	}

	// if we don't have last processed block, or it is less than startBlockFromGenesis,
	// we will start from startBlockFromGenesis
	lastProcessedBlock = max(lastProcessedBlock, config.StartBlockFromGenesis)

	if config.NumOfBlocksToReconcile > 0 {
		latestBlock, err := config.BlockProvider.GetBlockByNumber(ethgo.Latest, false)
		if err != nil {
			return nil, err
		}

		if latestBlock.Number > config.NumOfBlocksToReconcile &&
			lastProcessedBlock < latestBlock.Number-config.NumOfBlocksToReconcile {
			// if we missed too much blocks,
			// then we should start syncing from
			// latestBlock.Number - NumOfBlocksToReconcile
			lastProcessedBlock = latestBlock.Number - config.NumOfBlocksToReconcile
		}
	}

	return &EventTracker{
		config:         config,
		store:          store,
		blockTracker:   blocktracker.NewJSONBlockTracker(config.BlockProvider),
		blockContainer: NewTrackerBlockContainer(lastProcessedBlock),
		chainID:        chainID,
	}, nil
}

// Start is a method in the EventTracker struct that starts the tracking of blocks
// and retrieval of logs from given blocks from the tracked chain.
// If the tracker was turned off (node was down) for some time, it will sync up all the missed
// blocks and logs from the last start (in regards to NumOfBlocksToReconcile field in config).
// It should be called once the EventTracker is created and configured and probably from separate goroutine.
//
// Inputs:
// - ctx: A context.Context instance to manage cancellation and timeouts.
func (e *EventTracker) Start(ctx context.Context) {
	handleError := func(err error, msg string) error {
		e.config.Logger.Error(msg, "err", err)

		var netErr net.Error

		if errors.As(err, &netErr) && netErr.Timeout() {
			e.config.Logger.Warn("Timeout error occurred; attempting to recreate connection", "err", err)

			if serr := setupBlockProvider(e.config, true); serr != nil {
				e.config.Logger.Error("Failed to recreate connection after timeout", "err", serr)
			}
		}

		return err
	}

	e.config.Logger.Info("Starting event tracker",
		"jsonRpcEndpoint", e.config.RPCEndpoint,
		"numBlockConfirmations", e.config.NumBlockConfirmations,
		"pollInterval", e.config.PollInterval,
		"syncBatchSize", e.config.SyncBatchSize,
		"numOfBlocksToReconcile", e.config.NumOfBlocksToReconcile,
		"logFilter", e.config.LogFilter,
		"startBlockFromGenesis", e.config.StartBlockFromGenesis,
		"lastProcessedBlock", e.blockContainer.LastProcessedBlock(),
	)

	defer func() {
		e.config.Logger.Info("Event tracker stoped", "lastProcessedBlock", e.blockContainer.LastProcessedBlock())
	}()

	common.RetryForever(ctx, time.Second, func(context.Context) error {
		// sync up all missed blocks on start if it is not already sync up
		err := e.syncOnStart(ctx)
		if err != nil {
			return handleError(err, "Syncing up on start failed.")
		}

		// start the polling of blocks
		err = e.blockTracker.Track(ctx, func(block *ethgo.Block) error {
			return e.trackBlock(ctx, block)
		})

		return handleError(err, "Tracking blocks failed.")
	})
}

// trackBlock is a method in the EventTracker struct that is responsible for tracking blocks and processing their logs
//
// Inputs:
// - block: An instance of the ethgo.Block struct representing a block to track.
// - ctx: A context.Context instance to manage cancellation and timeouts.
//
// Returns:
//   - nil if tracking block passes successfully.
//   - An error if there is an error on tracking given block.
func (e *EventTracker) trackBlock(ctx context.Context, block *ethgo.Block) error {
	if !e.blockContainer.IsOutOfSync(block) {
		e.blockContainer.AcquireWriteLock()
		defer e.blockContainer.ReleaseWriteLock()

		if e.blockContainer.IsBlockFromThePastLocked(block) {
			e.config.Logger.Debug("Block is already processed or in the future",
				"lastProcessedBlock", e.blockContainer.LastProcessedBlockLocked(), "latestBlockFromRpc", block.Number)

			return nil // no need to get new state, since we are already up to date or in the future
		}

		if e.blockContainer.LastCachedBlock() < block.Number {
			// we are not out of sync, it's a sequential add of new block
			if err := e.blockContainer.AddBlock(block); err != nil {
				return err
			}
		}

		// check if some blocks reached confirmation level so that we can process their logs
		return e.processLogs()
	}

	// we are out of sync (either we missed some blocks, or a reorg happened)
	// so we get remove the old pending state and get the new one
	return e.getNewState(ctx, block)
}

// syncOnStart is a method in the EventTracker struct that is responsible
// for syncing the event tracker on startup.
// It retrieves the latest block and checks if the event tracker is out of sync.
// If it is out of sync, it calls the getNewState method to update the state.
//
// Input:
//   - ctx - context to cancel the operation if needed
//
// Returns:
//   - nil if sync passes successfully, or no sync is done.
//   - An error if there is an error retrieving blocks or logs from the external provider or saving logs to the store.
func (e *EventTracker) syncOnStart(ctx context.Context) (err error) {
	var latestBlock *ethgo.Block

	e.once.Do(func() {
		e.config.Logger.Info("Syncing up on start...")

		latestBlock, err = e.config.BlockProvider.GetBlockByNumber(ethgo.Latest, false)
		if err != nil {
			return
		}

		if !e.blockContainer.IsOutOfSync(latestBlock) {
			e.config.Logger.Info("Everything synced up on start")

			return
		}

		err = e.getNewState(ctx, latestBlock)
	})

	return err
}

// getNewState is called if tracker is out of sync (it missed some blocks),
// or a reorg happened in the tracked chain.
// It acquires write lock on the block container, so that the state is not changed while it
// retrieves the new blocks (new state).
// It will clean the previously cached state (non confirmed blocks), get the new state,
// set it on the block container and process logs on the confirmed blocks on the new state
//
// Input:
//   - latestBlock - latest block on the tracked chain
//   - ctx - context to cancel the operation if needed
//
// Returns:
//   - nil if there are no confirmed blocks.
//   - An error if there is an error retrieving blocks or logs from the external provider or saving logs to the store.
func (e *EventTracker) getNewState(ctx context.Context, latestBlock *ethgo.Block) error {
	e.blockContainer.AcquireWriteLock()
	defer e.blockContainer.ReleaseWriteLock()

	lastProcessedBlock := e.blockContainer.LastProcessedBlockLocked()

	e.config.Logger.Info("Getting new state, since some blocks were missed",
		"lastProcessedBlock", lastProcessedBlock, "latestBlockFromRpc", latestBlock.Number)

	if e.blockContainer.IsBlockFromThePastLocked(latestBlock) {
		return nil // no need to get new state, since we are already up to date or in the future
	}

	// if latest block already in memory -> exit
	if e.blockContainer.BlockExists(latestBlock) {
		return nil
	}

	startBlock := lastProcessedBlock + 1

	// sanitize startBlock from which we will start polling for blocks
	if e.config.NumOfBlocksToReconcile > 0 &&
		latestBlock.Number > e.config.NumOfBlocksToReconcile &&
		latestBlock.Number-e.config.NumOfBlocksToReconcile > lastProcessedBlock {
		startBlock = latestBlock.Number - e.config.NumOfBlocksToReconcile
	}

	// it is not optimal to start from the latest block if there are too many blocks for syncing
	if latestBlock.Number-startBlock+1 > max(e.config.NumBlockConfirmations, maxBlockGapForLatestSync) {
		return e.getNewStateFromFirst(ctx, startBlock, latestBlock)
	}

	return e.getNewStateFromLatest(ctx, startBlock, latestBlock)
}

func (e *EventTracker) getNewStateFromFirst(
	ctx context.Context, startBlock uint64, latestBlock *ethgo.Block,
) error {
	e.blockContainer.CleanState() // clean old state

	// get blocks in batches
	for i := startBlock; i < latestBlock.Number; i += e.config.SyncBatchSize {
		if err := checkIfContextDone(ctx); err != nil {
			return err
		}

		end := i + e.config.SyncBatchSize - 1
		if end > latestBlock.Number {
			// we go until the latest block, since we don't need to
			// query for it using an rpc point, since we already have it
			end = latestBlock.Number - 1
		}

		e.config.Logger.Info("Getting new state for block batch", "fromBlock", i, "toBlock", end)

		// get and add blocks in batch
		for j := i; j <= end; j++ {
			if err := checkIfContextDone(ctx); err != nil {
				return err
			}

			block, err := e.config.BlockProvider.GetBlockByNumber(ethgo.BlockNumber(j), false) //nolint:gosec
			if err != nil {
				e.config.Logger.Error("Getting new state for block batch failed on rpc call",
					"fromBlock", i,
					"toBlock", end,
					"currentBlock", j,
					"err", err)

				return err
			}

			if err := e.blockContainer.AddBlock(block); err != nil {
				return err
			}
		}

		// now process logs from confirmed blocks if any
		if err := e.processLogs(); err != nil {
			return err
		}
	}

	// add latest block only if reorg did not happen
	if err := e.blockContainer.AddBlock(latestBlock); err != nil {
		return err
	}

	// process logs if there are more confirmed events
	if err := e.processLogs(); err != nil {
		e.config.Logger.Error("Getting new state failed",
			"latestBlockFromRpc", latestBlock.Number,
			"err", err)

		return err
	}

	e.config.Logger.Info("Getting new state finished",
		"newLastProcessedBlock", e.blockContainer.LastProcessedBlockLocked(),
		"latestBlockFromRpc", latestBlock.Number)

	return nil
}

func (e *EventTracker) getNewStateFromLatest(
	ctx context.Context, startBlock uint64, latestBlock *ethgo.Block,
) error {
	blocksToAdd := []*ethgo.Block{latestBlock}

	for blockNum := latestBlock.Number - 1; blockNum >= startBlock; blockNum-- {
		if err := checkIfContextDone(ctx); err != nil {
			return err
		}

		block, err := e.config.BlockProvider.GetBlockByNumber(ethgo.BlockNumber(blockNum), false) //nolint:gosec
		if err != nil {
			e.config.Logger.Error("Getting block failed", "blockNum", blockNum, "err", err)

			return err
		}

		if e.blockContainer.BlockExists(block) {
			break
		}

		if blocksToAdd[len(blocksToAdd)-1].ParentHash != block.Hash {
			return fmt.Errorf("reorg happened during retrieving new state: block = %d", blockNum)
		}

		blocksToAdd = append(blocksToAdd, block)
	}

	slices.Reverse(blocksToAdd)
	// remove all blocks from memory that are not part of the current canonical chain
	e.blockContainer.RemoveAllAfterParentOf(blocksToAdd[0])
	// math.ceil(x/y) == (x+y-1)/x
	batchesCnt := (uint64(len(blocksToAdd)) + e.config.SyncBatchSize - 1) / e.config.SyncBatchSize
	offset := uint64(0)

	for i := uint64(0); i < batchesCnt; i++ {
		if err := checkIfContextDone(ctx); err != nil {
			return err
		}

		nextOffset := min(offset+e.config.SyncBatchSize, uint64(len(blocksToAdd)))

		for j := offset; j < nextOffset; j++ {
			if err := e.blockContainer.AddBlock(blocksToAdd[j]); err != nil {
				return err
			}
		}

		// process logs if there are more confirmed events
		if err := e.processLogs(); err != nil {
			e.config.Logger.Error("Getting new state failed",
				"latestBlockFromRpc", blocksToAdd[len(blocksToAdd)-1].Number,
				"err", err)

			return err
		}

		offset = nextOffset
	}

	e.config.Logger.Info("Getting new state finished",
		"newLastProcessedBlock", e.blockContainer.LastProcessedBlockLocked(),
		"latestBlockFromRpc", latestBlock.Number)

	return nil
}

// ProcessLogs retrieves logs for confirmed blocks, filters them based on certain criteria,
// passes them to the subscriber, and stores them in a store.
// It also removes the processed blocks from the block container.
//
// Returns:
// - nil if there are no confirmed blocks.
// - An error if there is an error retrieving logs from the external provider or saving logs to the store.
func (e *EventTracker) processLogs() error {
	confirmedBlocks := e.blockContainer.GetConfirmedBlocks(e.config.NumBlockConfirmations)
	if confirmedBlocks == nil {
		// no confirmed blocks, so nothing to process
		e.config.Logger.Debug("No confirmed blocks. Nothing to process")

		return nil
	}

	fromBlock := confirmedBlocks[0]
	toBlock := confirmedBlocks[len(confirmedBlocks)-1]

	e.config.Logger.Debug("Processing logs for blocks", "fromBlock", fromBlock, "toBlock", toBlock)

	logs, err := e.config.BlockProvider.GetLogs(e.getLogsQuery(fromBlock, toBlock))
	if err != nil {
		e.config.Logger.Error("Process logs failed on getting logs from rpc",
			"fromBlock", fromBlock,
			"toBlock", toBlock,
			"err", err)

		return err
	}

	filteredLogs := make([]*ethgo.Log, 0, len(logs))

	for _, log := range logs {
		logIDs, exist := e.config.LogFilter[log.Address]
		if !exist {
			continue
		}

		for _, id := range logIDs {
			if log.Topics[0] == id {
				filteredLogs = append(filteredLogs, log)

				if err := e.config.EventSubscriber.AddLog(e.chainID, log); err != nil {
					// we will only log this, since the store will have these logs
					// and subscriber can just get what he missed from store
					e.config.Logger.Error("An error occurred while passing event log to subscriber",
						"err", err)
				}

				break
			}
		}
	}

	if err := e.store.InsertLastProcessedBlock(toBlock); err != nil {
		e.config.Logger.Error("Process logs failed on saving last processed block",
			"fromBlock", fromBlock,
			"toBlock", toBlock,
			"err", err)

		return err
	}

	if err := e.store.InsertLogs(filteredLogs); err != nil {
		e.config.Logger.Error("Process logs failed on saving logs to store",
			"fromBlock", fromBlock,
			"toBlock", toBlock,
			"err", err)

		return err
	}

	if err := e.blockContainer.RemoveBlocks(fromBlock, toBlock); err != nil {
		return fmt.Errorf("could not remove processed blocks. Err: %w", err)
	}

	e.config.Logger.Debug("Processing logs for blocks finished",
		"fromBlock", fromBlock,
		"toBlock", toBlock,
		"numOfLogs", len(filteredLogs))

	return nil
}

// getLogsQuery is a method of the EventTracker struct that creates and returns
// a LogFilter object with the specified block range.
//
// Input:
//   - from (uint64): The starting block number for the log filter.
//   - to (uint64): The ending block number for the log filter.
//
// Returns:
//   - filter (*ethgo.LogFilter): The created LogFilter object with the specified block range.
func (e *EventTracker) getLogsQuery(from, to uint64) *ethgo.LogFilter {
	addresses := make([]ethgo.Address, 0, len(e.config.LogFilter))
	for a := range e.config.LogFilter {
		addresses = append(addresses, a)
	}

	filter := &ethgo.LogFilter{Address: addresses}
	filter.SetFromUint64(from)
	filter.SetToUint64(to)

	return filter
}

// setupBlockProvider initializes or resets the BlockProvider for the EventTrackerConfig.
// If the BlockProvider is already set and the force flag is false, it does nothing.
// Otherwise, it ensures the RPCClient is properly closed (if it exists) and creates a new
// JSON-RPC client using the provided RPCEndpoint. The newly created client is then used
// to set up the BlockProvider.
//
// Input:
//   - config (*EventTrackerConfig): A pointer to EventTrackerConfig containing the configuration details.
//   - force (bool): A boolean flag that forces reinitialization of the BlockProvider even if it exists.
//
// Returns:
//   - an error if the JSON-RPC client creation fails, or nil on success.
func setupBlockProvider(config *EventTrackerConfig, force bool) error {
	if config.BlockProvider != nil && !force {
		return nil
	}

	if config.RPCClient != nil {
		_ = config.RPCClient.Close() // try to close the previous transfer
	}

	clt, err := jsonrpc.NewClient(config.RPCEndpoint)
	if err != nil {
		return err
	}

	config.RPCClient = clt
	config.BlockProvider = clt.Eth()

	return nil
}

// checkIfContextDone checks if the context is done and returns an error if it is.
// does not block, just checks if the context is done or not.
func checkIfContextDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
