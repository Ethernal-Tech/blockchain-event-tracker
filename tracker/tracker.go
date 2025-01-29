package tracker

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"time"

	eventStore "github.com/Ethernal-Tech/blockchain-event-tracker/store"
	"github.com/Ethernal-Tech/ethgo"
	"github.com/Ethernal-Tech/ethgo/jsonrpc"
	hcf "github.com/hashicorp/go-hclog"
)

// EventSubscriber is an interface that defines methods for handling tracked logs (events) from a blockchain
type EventSubscriber interface {
	AddLog(chainID *big.Int, log *ethgo.Log) error
}

// BlockProvider is an interface that defines methods for retrieving blocks and logs from a blockchain
type BlockProvider interface {
	GetBlockByNumber(i ethgo.BlockNumber, full bool) (*ethgo.Block, error)
	GetLogs(filter *ethgo.LogFilter) ([]*ethgo.Log, error)
	ChainID() (*big.Int, error)
}

// EventTrackerConfig is a struct that holds configuration of a EventTracker
type EventTrackerConfig struct {
	// RPCEndpoint is the full json rpc url on some node on a tracked chain
	RPCEndpoint string `json:"rpcEndpoint"`

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

	closeCh chan struct{}

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
//   - startBlockFromGenesis: block from which to start syncing
//
// Outputs:
//   - A new instance of the EventTracker struct.
func NewEventTracker(config *EventTrackerConfig, store eventStore.EventTrackerStore,
	startBlockFromGenesis uint64) (*EventTracker, error) {
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

	updateLastProcessedBlock := false
	if lastProcessedBlock < startBlockFromGenesis {
		// if we don't have last processed block, or it is less than startBlockFromGenesis,
		// we will start from startBlockFromGenesis
		lastProcessedBlock = startBlockFromGenesis
		updateLastProcessedBlock = true
	}

	if config.NumOfBlocksToReconcile > 0 {
		latestBlock, err := config.BlockProvider.GetBlockByNumber(ethgo.Finalized, false)
		if err != nil {
			return nil, err
		}

		if latestBlock.Number > config.NumOfBlocksToReconcile &&
			lastProcessedBlock < latestBlock.Number-config.NumOfBlocksToReconcile {
			// if we missed too much blocks,
			// then we should start syncing from
			// latestBlock.Number - NumOfBlocksToReconcile
			lastProcessedBlock = latestBlock.Number - config.NumOfBlocksToReconcile
			updateLastProcessedBlock = true
		}
	}

	if updateLastProcessedBlock {
		if err := store.InsertLastProcessedBlock(lastProcessedBlock); err != nil {
			return nil, fmt.Errorf("failed to update last processed block: %w", err)
		}
	}

	return &EventTracker{
		config:  config,
		store:   store,
		closeCh: make(chan struct{}),
		chainID: chainID,
	}, nil
}

// Start is a method in the EventTracker struct that starts the tracking of blocks
// and retrieval of logs from given blocks from the tracked chain.
// If the tracker was turned off (node was down) for some time, it will sync up all the missed
// blocks and logs from the last start (in regards to NumOfBlocksToReconcile field in config).
//
// Returns:
//   - nil if start passes successfully.
//   - An error if there is an error on startup of blocks tracking on tracked chain.
func (e *EventTracker) Start() error {
	e.config.Logger.Info("Starting event tracker",
		"jsonRpcEndpoint", e.config.RPCEndpoint,
		"pollInterval", e.config.PollInterval,
		"syncBatchSize", e.config.SyncBatchSize,
		"numOfBlocksToReconcile", e.config.NumOfBlocksToReconcile,
		"logFilter", e.config.LogFilter,
	)

	ctx, cancelFn := context.WithCancel(context.Background())
	go func() {
		<-e.closeCh
		cancelFn()
	}()

	lastFinalizedBlock, err := e.config.BlockProvider.GetBlockByNumber(ethgo.Finalized, false)
	if err != nil {
		return fmt.Errorf("failed to get last finalized block: %w", err)
	}

	lastProcessedBlock, err := e.store.GetLastProcessedBlock()
	if err != nil {
		return fmt.Errorf("failed to get last processed block from store: %w", err)
	}

	lastFinalizedBlockNumber := lastFinalizedBlock.Number

	if lastFinalizedBlockNumber < lastProcessedBlock {
		// this should never happen if we are syncing the same network
		return fmt.Errorf("last finalized block: %d is less than last processed block: %d",
			lastFinalizedBlockNumber, lastProcessedBlock)
	}

	for {
		select {
		case <-ctx.Done():
			e.config.Logger.Info("Event tracker stopped")
			return nil
		default:
		}

		fromBlock := lastProcessedBlock + 1
		toBlock := fromBlock + e.config.SyncBatchSize

		numBatches := int(math.Ceil(float64(lastFinalizedBlockNumber-fromBlock+1) / float64(e.config.SyncBatchSize)))

		for i := 0; i < numBatches; i++ {
			if toBlock > lastFinalizedBlock.Number {
				toBlock = lastFinalizedBlock.Number
			}

			if err := e.getBlockRange(fromBlock, toBlock); err != nil {
				return fmt.Errorf("failed to get new state for batch: %w", err)
			}

			lastProcessedBlock = toBlock
			fromBlock = toBlock + 1
			toBlock += e.config.SyncBatchSize
		}

		lastFinalizedBlockNumber = e.waitForNewFinalizedBlock(ctx, lastFinalizedBlockNumber)
	}
}

// waitForNewFinalizedBlock is waits for a new finalized block to appear on tracked network
func (e *EventTracker) waitForNewFinalizedBlock(ctx context.Context, lastSeenBlock uint64) uint64 {
	ticker := time.NewTicker(e.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.config.Logger.Info("WaitForNewFinalizedBlock - context cancelled")

			return lastSeenBlock
		case <-ticker.C:
			block, err := e.config.BlockProvider.GetBlockByNumber(ethgo.Finalized, false)
			if err != nil {
				e.config.Logger.Error("error getting last finalized block num: ", err)

				continue
			}

			if block.Number > lastSeenBlock {
				return block.Number
			}
		}
	}
}

// getBlockRange is a method of the EventTracker struct that retrieves logs from the specified block range
func (e *EventTracker) getBlockRange(fromBlock, toBlock uint64) error {
	logs, err := e.config.BlockProvider.GetLogs(e.getLogsQuery(fromBlock, toBlock))
	if err != nil {
		e.config.Logger.Error("getting block range failed",
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

	e.config.Logger.Debug("getting block range finished",
		"fromBlock", fromBlock,
		"toBlock", toBlock,
		"numOfLogs", len(filteredLogs))

	return nil
}

// Close closes the EventTracker by closing the closeCh channel.
// This method is used to signal the goroutines to stop.
//
// Example Usage:
//
//	tracker := NewEventTracker(config)
//	tracker.Start()
//	defer tracker.Close()
//
// Inputs: None
//
// Flow:
//  1. The Close() method is called on an instance of EventTracker.
//  2. The closeCh channel is closed, which signals the goroutines to stop.
//
// Outputs: None
func (e *EventTracker) Close() {
	close(e.closeCh)
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
