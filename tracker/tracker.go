package tracker

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/Ethernal-Tech/blockchain-event-tracker/common"
	eventStore "github.com/Ethernal-Tech/blockchain-event-tracker/store"
	"github.com/Ethernal-Tech/ethgo"
	"github.com/Ethernal-Tech/ethgo/jsonrpc"
	hcf "github.com/hashicorp/go-hclog"
)

// EventSubscriber is an interface that defines methods for handling tracked logs (events) from a blockchain
type EventSubscriber interface {
	AddLog(chainID *big.Int, log *ethgo.Log) error
}

// Provider is everything the tracker needs from a node on the tracked chain.
// BlockNumber reports the latest block, GetBlockByNumber resolves a block tag such
// as finalized, and the remaining two retrieve the logs and identify the chain.
type Provider interface {
	BlockNumber() (uint64, error)
	GetBlockByNumber(i ethgo.BlockNumber, full bool) (*ethgo.Block, error)
	GetLogs(filter *ethgo.LogFilter) ([]*ethgo.Log, error)
	ChainID() (*big.Int, error)
}

// ConfirmationStrategy defines how the tracker determines the last block on the
// tracked chain whose logs are considered safe to process.
type ConfirmationStrategy string

const (
	// ConfirmationStrategyNumBlockConfirmations derives the confirmed block from the
	// latest block on the tracked chain, reduced by NumBlockConfirmations.
	// It does not rely on the tracked chain reporting finality.
	ConfirmationStrategyNumBlockConfirmations ConfirmationStrategy = "numBlockConfirmations"

	// ConfirmationStrategyFinalized uses the finalized block reported by the tracked
	// chain. NumBlockConfirmations is not applied, since such a block is already final.
	ConfirmationStrategyFinalized ConfirmationStrategy = "finalized"
)

// EventTrackerConfig is a struct that holds configuration of a EventTracker
type EventTrackerConfig struct {
	// RPCEndpoint is the full json rpc url on some node on a tracked chain
	RPCEndpoint string `json:"rpcEndpoint"`

	// ConfirmationStrategy defines how the confirmed block is determined.
	// An empty value defaults to ConfirmationStrategyNumBlockConfirmations,
	// which keeps the behavior of configurations that do not set this field.
	ConfirmationStrategy ConfirmationStrategy `json:"confirmationStrategy"`

	// NumBlockConfirmations defines how many blocks must pass from a certain block,
	// to consider that block as final on the tracked chain, and its logs safe to process.
	// The tracker does not verify that assumption, so set it deep enough that a reorg
	// is not expected to reach below it.
	// (e.g., NumBlockConfirmations = 3, and if the latest block on the tracked chain is 10,
	// logs are processed up to and including block 7)
	// It is only used by ConfirmationStrategyNumBlockConfirmations.
	NumBlockConfirmations uint64 `json:"numBlockConfirmations"`

	// SyncBatchSize defines how many blocks one getLogs call covers, so it has to stay
	// within the block range the tracked node accepts. It must be greater than zero.
	// (e.g., SyncBatchSize = 10, the last processed block is 10, the last confirmed block
	// on the tracked chain is 100, it will get logs from blocks 11-20, save them together
	// with the new last processed block, and continue to the next batch)
	SyncBatchSize uint64 `json:"syncBatchSize"`

	// PollInterval defines a time interval in which tracker polls json rpc node
	// for latest block on the tracked chain.
	PollInterval time.Duration `json:"pollInterval"`

	// LogFilter defines which events are tracked and from which contracts on the tracked chain
	LogFilter map[ethgo.Address][]ethgo.Hash `json:"logFilter"`

	// StartBlockFromGenesis defines the block from which the tracker starts tracking events
	// when the persisted checkpoint is behind it. It never moves the tracker backwards.
	StartBlockFromGenesis uint64 `json:"startBlockFromGenesis"`

	// Logger is the logger instance for event tracker
	Logger hcf.Logger `json:"-"`

	// Provider returns blocks and logs from the tracked chain. When it is not set,
	// the tracker creates an ethgo json rpc client for RPCEndpoint.
	Provider Provider `json:"-"`

	// Client is the jsonrpc client
	RPCClient *jsonrpc.Client `json:"-"`

	// EventSubscriber is the subscriber that requires events tracked by the event tracker
	EventSubscriber EventSubscriber `json:"-"`
}

var defaultStore = "./eventStore.db"

// EventTracker represents a tracker for events on desired contracts on some chain
type EventTracker struct {
	config *EventTrackerConfig

	store eventStore.EventTrackerStore

	chainID *big.Int
}

// NewEventTracker is a constructor function that creates a new instance of the EventTracker struct.
//
// Example Usage:
//
//	config := &EventTrackerConfig{
//		RPCEndpoint:           "http://some-json-rpc-url.com",
//		StartBlockFromGenesis: 100_000,
//		NumBlockConfirmations: 10,
//		SyncBatchSize:         20,
//		PollInterval:          2 * time.Second,
//		Logger:                logger,
//		EventSubscriber:       subscriber,
//		LogFilter: map[ethgo.Address][]ethgo.Hash{
//			addressOfSomeContract: {idHashOfSomeEvent},
//		},
//	}
//
//	t, err := NewEventTracker(config, store)
//
// Inputs:
//   - config (*EventTrackerConfig): configuration of EventTracker.
//   - store: implementation of EventTrackerStore interface. When it is nil,
//     a BoltDB store is created at defaultStore.
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

	if config.ConfirmationStrategy == "" {
		config.ConfirmationStrategy = ConfirmationStrategyNumBlockConfirmations
	}

	if config.ConfirmationStrategy != ConfirmationStrategyNumBlockConfirmations &&
		config.ConfirmationStrategy != ConfirmationStrategyFinalized {
		return nil, fmt.Errorf("unknown confirmation strategy: %s", config.ConfirmationStrategy)
	}

	if config.PollInterval == 0 {
		config.PollInterval = 3 * time.Second
	}

	if store == nil {
		var err error

		store, err = eventStore.NewBoltDBEventTrackerStore(defaultStore)
		if err != nil {
			return nil, err
		}
	}

	// if the provider is not provided externally, we can start the ethgo one
	if err := setupProvider(config, false); err != nil {
		return nil, err
	}

	if config.SyncBatchSize == 0 {
		return nil, errors.New("sync batch size must be greater than zero")
	}

	chainID, err := config.Provider.ChainID()
	if err != nil {
		return nil, err
	}

	return &EventTracker{
		config:  config,
		store:   store,
		chainID: chainID,
	}, nil
}

// Start is a method in the EventTracker struct that starts the retrieval of logs
// from confirmed blocks on the tracked chain.
// It always resumes from the persisted last-processed-block checkpoint, so a tracker
// that was turned off (node was down) for some time syncs up every confirmed block it missed.
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

			if serr := setupProvider(e.config, true); serr != nil {
				e.config.Logger.Error("Failed to recreate connection after timeout", "err", serr)
			}
		}

		return err
	}

	e.config.Logger.Info("Starting event tracker",
		"jsonRpcEndpoint", e.config.RPCEndpoint,
		"confirmationStrategy", e.config.ConfirmationStrategy,
		"numBlockConfirmations", e.config.NumBlockConfirmations,
		"pollInterval", e.config.PollInterval,
		"syncBatchSize", e.config.SyncBatchSize,
		"logFilter", e.config.LogFilter,
		"startBlockFromGenesis", e.config.StartBlockFromGenesis,
		"lastProcessedBlock", e.lastProcessedBlock(),
	)

	defer func() {
		e.config.Logger.Info("Event tracker stoped", "lastProcessedBlock", e.lastProcessedBlock())
	}()

	common.RetryForever(ctx, time.Second, func(context.Context) error {
		err := e.trackLogs(ctx)

		return handleError(err, "Tracking logs failed.")
	})
}

// lastProcessedBlock reads the persisted checkpoint for logging purposes only.
func (e *EventTracker) lastProcessedBlock() uint64 {
	lastProcessedBlock, err := e.store.GetLastProcessedBlock()
	if err != nil {
		e.config.Logger.Error("Could not read last processed block", "err", err)

		return 0
	}

	return lastProcessedBlock
}

// trackLogs processes all currently confirmed log ranges, then polls for new
// confirmed ranges until the context is cancelled.
func (e *EventTracker) trackLogs(ctx context.Context) error {
	if err := e.processAvailableLogs(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(e.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := e.processAvailableLogs(ctx); err != nil {
				return err
			}
		}
	}
}

// getConfirmedToBlock returns the number of the last block on the tracked chain
// whose logs are considered safe to process, according to the configured
// confirmation strategy. It returns zero when the tracked chain does not have
// such a block yet.
func (e *EventTracker) getConfirmedToBlock() (uint64, error) {
	if e.config.ConfirmationStrategy == ConfirmationStrategyFinalized {
		finalizedBlock, err := getBlockByNumber(e.config.Provider, ethgo.Finalized)
		if err != nil {
			return 0, err
		}

		return finalizedBlock.Number, nil
	}

	latestBlock, err := e.config.Provider.BlockNumber()
	if err != nil {
		return 0, err
	}

	if latestBlock <= e.config.NumBlockConfirmations {
		return 0, nil
	}

	return latestBlock - e.config.NumBlockConfirmations, nil
}

// processAvailableLogs processes logs after the persisted checkpoint and up to
// the last block confirmed by the configured confirmation strategy.
func (e *EventTracker) processAvailableLogs(ctx context.Context) error {
	confirmedToBlock, err := e.getConfirmedToBlock()
	if err != nil {
		return err
	}

	if confirmedToBlock == 0 {
		return nil
	}

	lastProcessedBlock, err := e.store.GetLastProcessedBlock()
	if err != nil {
		return err
	}

	lastProcessedBlock = max(lastProcessedBlock, e.config.StartBlockFromGenesis)

	if lastProcessedBlock >= confirmedToBlock {
		return nil
	}

	for fromBlock := lastProcessedBlock + 1; fromBlock <= confirmedToBlock; {
		if err := checkIfContextDone(ctx); err != nil {
			return err
		}

		toBlock := confirmedToBlock
		if confirmedToBlock-fromBlock >= e.config.SyncBatchSize {
			toBlock = fromBlock + e.config.SyncBatchSize - 1
		}

		if err := e.processLogsRange(fromBlock, toBlock); err != nil {
			return err
		}

		if toBlock == confirmedToBlock {
			break
		}

		fromBlock = toBlock + 1
	}

	return nil
}

// processLogsRange retrieves, filters, publishes and stores logs for the inclusive
// block range, then advances the persisted last-processed-block checkpoint.
func (e *EventTracker) processLogsRange(fromBlock, toBlock uint64) error {
	if fromBlock > toBlock {
		return fmt.Errorf("invalid log range: from block %d is greater than to block %d", fromBlock, toBlock)
	}

	e.config.Logger.Debug("Processing logs for blocks", "fromBlock", fromBlock, "toBlock", toBlock)

	logs, err := e.config.Provider.GetLogs(e.getLogsQuery(fromBlock, toBlock))
	if err != nil {
		e.config.Logger.Error("Process logs failed on getting logs from rpc",
			"fromBlock", fromBlock,
			"toBlock", toBlock,
			"err", err)

		return err
	}

	filteredLogs := make([]*ethgo.Log, 0, len(logs))
	seenLogs := make(map[logIdentity]struct{}, len(logs))

	for _, log := range logs {
		if log == nil {
			continue
		}

		logIDs, exist := e.config.LogFilter[log.Address]
		if !exist || len(log.Topics) == 0 {
			continue
		}

		for _, id := range logIDs {
			if log.Topics[0] == id {
				identity := getLogIdentity(log)
				if _, seen := seenLogs[identity]; seen {
					break
				}
				seenLogs[identity] = struct{}{}

				storedLog, err := e.store.GetLog(log.BlockNumber, log.LogIndex)
				if err != nil {
					return fmt.Errorf("could not check whether log was already processed: %w", err)
				}
				if storedLog != nil && getLogIdentity(storedLog) == identity {
					break
				}

				if err := e.config.EventSubscriber.AddLog(e.chainID, log); err != nil {
					e.config.Logger.Error("An error occurred while passing event log to subscriber",
						"err", err)

					// Keep successfully published logs durable without advancing
					// the checkpoint. A retry can then skip them and resume with
					// the log that failed.
					if persistErr := e.store.InsertLogs(filteredLogs); persistErr != nil {
						return fmt.Errorf(
							"could not pass event log to subscriber: %w; "+
								"could not persist previously published logs: %v",
							err, persistErr)
					}

					return fmt.Errorf("could not pass event log to subscriber: %w", err)
				}

				filteredLogs = append(filteredLogs, log)

				break
			}
		}
	}

	if err := e.store.InsertLogsAndLastProcessedBlock(filteredLogs, toBlock); err != nil {
		e.config.Logger.Error("Process logs failed on saving logs and last processed block",
			"fromBlock", fromBlock,
			"toBlock", toBlock,
			"err", err)

		return err
	}

	e.config.Logger.Debug("Processing logs for blocks finished",
		"fromBlock", fromBlock,
		"toBlock", toBlock,
		"numOfLogs", len(filteredLogs))

	return nil
}

type logIdentity struct {
	blockHash       ethgo.Hash
	transactionHash ethgo.Hash
	logIndex        uint64
}

func getLogIdentity(log *ethgo.Log) logIdentity {
	return logIdentity{
		blockHash:       log.BlockHash,
		transactionHash: log.TransactionHash,
		logIndex:        log.LogIndex,
	}
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

// setupProvider initializes or resets the Provider for the EventTrackerConfig.
// If the Provider is already set and the force flag is false, it does nothing.
// Otherwise, it ensures the RPCClient is properly closed (if it exists) and creates a new
// JSON-RPC client using the provided RPCEndpoint. The newly created client is then used
// to set up the Provider.
//
// Input:
//   - config (*EventTrackerConfig): A pointer to EventTrackerConfig containing the configuration details.
//   - force (bool): A boolean flag that forces reinitialization of the Provider even if it exists.
//
// Returns:
//   - an error if the JSON-RPC client creation fails, or nil on success.
func setupProvider(config *EventTrackerConfig, force bool) error {
	if config.Provider != nil && !force {
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
	config.Provider = clt.Eth()

	return nil
}

// getBlockByNumber retrieves a block from the tracked chain and guarantees that,
// when no error is returned, the returned block is not nil.
// A json rpc node answers with a null block, and no error, for a block it does not have.
// This happens, for example, when the tracked chain does not support the requested
// block tag, or when the endpoint is load balanced and the request is served by a
// node that lags behind the one which reported the latest block.
// Such a result is turned into an error here, instead of being propagated to the callers.
//
// Input:
//   - provider (Provider): provider that returns blocks from the tracked chain.
//   - blockNumber (ethgo.BlockNumber): the number, or the tag, of the block to retrieve.
//
// Returns:
//   - the requested block, if the tracked chain has it.
//   - an error if the rpc call failed, or if the tracked chain does not have the given block.
func getBlockByNumber(provider Provider, blockNumber ethgo.BlockNumber) (*ethgo.Block, error) {
	block, err := provider.GetBlockByNumber(blockNumber, false)
	if err != nil {
		return nil, err
	}

	if block == nil {
		return nil, fmt.Errorf("block %s not found on the tracked chain", blockNumber.String())
	}

	return block, nil
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
