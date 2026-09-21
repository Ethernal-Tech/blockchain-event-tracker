# Event Tracker

Event Tracker retrieves events (logs) emitted by smart contracts on an EVM blockchain and hands them to your application. It polls a json rpc node, reads logs only from blocks it considers confirmed, and remembers how far it got, so it resumes from there after a restart.

## Table of Contents

- [Event Tracker](#event-tracker)
  - [Table of Contents](#table-of-contents)
  - [Introduction](#introduction)
  - [How it works](#how-it-works)
  - [Features](#features)
  - [What it does not do](#what-it-does-not-do)
    - [Prerequisites](#prerequisites)
    - [Installation](#installation)
  - [Usage](#usage)
    - [Configuring the Tracker](#configuring-the-tracker)
    - [Tracking events](#tracking-events)
  - [The database](#the-database)
  - [Contributing](#contributing)
  - [License](#license)

## Introduction

Smart contracts emit events to report their execution and state changes. Event Tracker turns those events into a stream your application can consume, without you having to deal with polling, batching, checkpointing and restarts.

It is a log indexer, and nothing more. It does not follow the chain block by block, and it keeps no in-memory model of the chain.

## How it works

On every poll interval the tracker performs one cycle:

1. It determines `confirmedTo`, the last block whose logs are safe to read. This costs one rpc call, and which call it is depends on `ConfirmationStrategy`.
2. It reads the last processed block from the store. That value is the checkpoint, and it means that logs up to and including that block were already delivered.
3. If the checkpoint already reached `confirmedTo`, the cycle ends here.
4. Otherwise it walks the range from `checkpoint + 1` to `confirmedTo` in steps of `SyncBatchSize`, spending one `eth_getLogs` call per step.
5. For every log that matches `LogFilter`, it skips duplicates inside the same rpc response, skips logs the store already holds, and hands the rest to `EventSubscriber`.
6. It then writes the logs of that step and the new checkpoint in a single database transaction, and moves to the next step.

`ConfirmationStrategy` chooses how step one computes the boundary:

| Strategy | Boundary | Rpc call |
| --- | --- | --- |
| `numBlockConfirmations` | latest block minus `NumBlockConfirmations` | `eth_blockNumber` |
| `finalized` | the block the chain itself reports as finalized | `eth_getBlockByNumber("finalized")` |

The default is `numBlockConfirmations`, which keeps working on chains that do not report finality. With `finalized`, `NumBlockConfirmations` is not applied, since such a block is already final, and the boundary lags further behind the head in exchange for a guarantee from consensus instead of an assumption. Not every chain and not every node supports that block tag. A node that does not support it answers with a null block and no error, and the tracker turns that into an error rather than treating the boundary as block zero.

Once it is caught up, a cycle costs two rpc calls: one for the boundary and one for the logs.

## Features

- **Confirmed log delivery**: only blocks below the confirmation boundary are read, so your application is not exposed to the unstable tip of the chain.

- **Durable checkpoints**: logs and the checkpoint are written in one transaction, so a crash can not advance the checkpoint past logs that were never stored.

- **Resumes where it stopped**: on start it continues from the stored checkpoint, and syncs up every confirmed block it missed, however long it was down.

- **Flexible configuration**: you choose the contracts and the events you care about, and how the confirmation boundary is computed.

## What it does not do

- **It does not detect reorgs.** With `numBlockConfirmations`, that the boundary is deep enough is an assumption you make through configuration. If a reorg reaches below it, the tracker will not notice, and the logs it already delivered will not be revoked. Use `finalized` when you need the chain to guarantee the boundary instead.

- **It does not look above the boundary.** Logs from more recent blocks are not read at all, so the delivery always lags the head by at least the configured confirmations.

- **It does not guarantee exactly once delivery.** The tracker recognizes an already delivered log by its block hash, transaction hash and log index, and skips it. That covers a restart and a retry after a failing subscriber. It does not cover the case where the subscriber accepted the logs of a batch and the database write then failed, because such a batch is retried whole. Your `EventSubscriber` has to tolerate seeing the same log twice.

### Prerequisites

Before using Event Tracker, make sure you have the following prerequisites:

- Go: Event Tracker is built with Go, so you'll need it installed on your system. You can download it from [go.dev](https://go.dev/doc/install).

- Public Node: Connect Event Tracker to a public node on the tracked chain (e.g., Geth, Edge) to access blockchain data using JSON RPC calls.

### Installation

Event Tracker is just a library, and is currently not intended to be used as a standalone application. You can use it by referencing the latest commit on `main` branch, and then start the event tracker as a part of your application.

## Usage

### Configuring the Tracker

1. You can keep the configuration in a file (e.g., config.json) and load it into the `EventTrackerConfig` struct which is passed to the Event Tracker instantiation.

   ```json
   {
     "rpcEndpoint": "https://your-node-url.com",
     "confirmationStrategy": "numBlockConfirmations",
     "numBlockConfirmations": 5,
     "syncBatchSize": 10,
     "pollInterval": 2000000000,
     "startBlockFromGenesis": 0,
     "logFilter": {
       "0xContractAddress": ["0xEventSig1", "0xEventSig2"]
     }
   }
   ```

   Note that `pollInterval` is a `time.Duration`, so in json it is a number of nanoseconds, and that `logFilter` is a map keyed by contract address.

2. Or you can just specify the configuration in code:

```go
    tracker, err := tracker.NewEventTracker(&tracker.EventTrackerConfig{
        RPCEndpoint:           "https://some-url.com",
        NumBlockConfirmations: 5,
        SyncBatchSize:         10,
        PollInterval:          2 * time.Second,
        LogFilter:             logFilter,
        EventSubscriber:       subscriber,
    }, store)
```

Our recommendations are:
- `ConfirmationStrategy` - leave it unset, or set it to `numBlockConfirmations`, unless the tracked chain reports finality and you prefer that guarantee over a shorter delay.
- `NumBlockConfirmations` - set this to the number of blocks you feel are enough to consider a block final on the tracked chain, meaning that it will not be replaced in a reorg. It is only used by the `numBlockConfirmations` strategy.
- `SyncBatchSize` - this is how many blocks one `eth_getLogs` call covers, so keep it under whatever range your node accepts, and remember that a wider range means fewer calls but a bigger response. It has to be greater than zero.
- `PollInterval` - should be configured to about the same as the block minting time on the tracked chain.
- `StartBlockFromGenesis` - the block the tracker starts from when the stored checkpoint is behind it. It never moves the tracker backwards.
- `Logger` - you can pass your own logger here, as long as it implements the `Logger` interface from `go-hclog`.
- `Store` - you can pass your own store (as long as it implements the `EventTrackerStore` interface), or use the provided `BoltDBEventTrackerStore` from this repo, that creates and uses a `BoltDB` instance to store the tracked events and the checkpoint.
- `Provider` - it's basically a json rpc client connected to the provided public node (`RPCEndpoint`), used to poll block and event data from tracked blockchain. When it is left unset, the tracker creates one for `RPCEndpoint` itself.
- `EventSubscriber` - here you plugin your custom code for handling tracked events. It has to tolerate receiving the same log more than once.
- `LogFilter` - here you configure which events (logs) on which contracts are going to be tracked. This is a map, where key is the contract address, and values are event signatures (event signatures are just hashed signatures of events, for example, if we have an event like this:
    ```solidity
        event SomeEvent(uint256 indexed id, address indexed sender, address indexed receiver, bytes data);
    ```
    then, its signature is: `keccak256(abi.Encode("SomeEvent(uint256,address,address,bytes))"`
)

### Tracking events

Start the Event Tracker by calling the `Start` function. It blocks and retries until the context is cancelled, so run it in its own goroutine:

```go
    eventTracker, err := tracker.NewEventTracker(config, store)
    if err != nil {
        return err
    }

    go eventTracker.Start(ctx)
```

For every tracked event, this method is called on `EventSubscriber` to handle it:

```go
    eventTracker.config.EventSubscriber.AddLog(chainID, log)
```

If `AddLog` returns an error, the checkpoint is not advanced, the logs accepted so far are still stored, and the batch is retried, skipping what was already delivered. Every delivered log is also saved in the store, so your application can query it later.

## The database

The store keeps two things, the checkpoint and the tracked logs, in a `BoltDB` file:

```text
bucket   lastProcessedTrackerBucket   key    lastProcessedTrackerBlock
bucket   logs                         key    uint64(blockNumber) || uint64(logIndex)
value    the log, as json
```

Block numbers and log indices are big endian, and the checkpoint is the number of the last block whose logs were processed.

This layout is a compatibility contract, because a tracker is normally deployed over a database an earlier version created, and because other applications read the same file. Renaming a bucket, or changing how a key is built, orphans every existing database. The tests in `store/legacy_compatibility_test.go` check both directions, that the store reads a database it has never written to, and that what it writes lands under these names and keys.

## Contributing
We welcome contributions to Event Tracker! If you have ideas for improvements or find bugs, please open an issue or submit a pull request.

## License
Event Tracker is licensed under the MIT License.
