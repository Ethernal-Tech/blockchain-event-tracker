# Event Tracker

Event Tracker is a powerful tool designed to track events emitted by smart contracts during transaction execution on an EVM blockchain. Whether you're a developer debugging your smart contracts, creating your own bridge, or you're a blockchain enthusiast interested in monitoring contract interactions, Event Tracker simplifies the process of event monitoring and analysis.

## Table of Contents

- [Event Tracker](#event-tracker)
  - [Table of Contents](#table-of-contents)
  - [Introduction](#introduction)
  - [Features](#features)
    - [Prerequisites](#prerequisites)
    - [Installation](#installation)
  - [Usage](#usage)
    - [Configuring the Tracker](#configuring-the-tracker)
    - [Tracking events](#tracking-events)
  - [Contributing](#contributing)
  - [License](#license)

## Introduction

Smart contracts on the Ethereum blockchain often emit events to provide insights into their execution and state changes. Event Tracker simplifies the process of monitoring and analyzing these events, making it a useful tool for EVM developers and enthusiasts.

## Features

- **Real-time Event Tracking**: Event Tracker provides real-time monitoring of events emitted by smart contracts, enabling developers to react promptly to contract interactions.

- **Flexible Configuration**: Customize event tracking by specifying the contracts and events of interest, ensuring that you receive only the data you need.

- **Data Storage**: Store event data locally for future analysis and reference.

### Prerequisites

Before using Event Tracker, make sure you have the following prerequisites:

- Go: Event Tracker is built with Go, so you'll need it installed on your system. You can download it from [go.dev](https://go.dev/doc/install).

- Public Node: Connect Event Tracker to a public node on the tracked chain (e.g., Geth, Edge) to access blockchain data using JSON RPC calls.

### Installation

Event Tracker is just a library, and is currently not intended to be used as a standalone application. You can use it by referencing the latest commit on `main` branch, and then start the event tracker as a part of your application.

## Usage

Event Tracker can be used to monitor, track, and analyze events emitted by smart contracts. Here's how to get started:

### Configuring the Tracker

1. You can create a configuration file (e.g., config.json) to specify the public node URL and the contracts and events you want to track, as well as the rest of the configuration parameters, and then load the configuration file into the `EventTrackerConfig` struct which is passed to the Event Tracker instantiation.

   ```json
   {
     "rpcEndpoint": "https://your-node-url.com",
     "pollTime": "2s",
     "syncBatchSize": 10,
     "numBlockConfirmations": 5,
     "maxBacklogSize": 10000,
     "logFilter": [
       {
         "0xContractAddress": ["EventSig1", "EventSig2"]
       }
     ]
     ...
   }
   ```

2. Or you can just specify the configuration in code:

```go
    tracker := NewEventTracker(&EventTrackerConfig{
        RPCEndpoint: "https://some-url.com",
        PollTime: 10*time.Second,
        SyncBatchSize: 10,
        ...
    })
```

Our recommendations are:
- `NumBlockConfirmations` - set this to the number of blocks you feel are enough to consider a block final on the tracked chain (where he will not be replaced in a reorg).
- `SyncBatchSize` - should be connected to the configured NumBlockConfirmations. For example, if the NumBlockConfirmations is 10, batch size should be around 25, meaning that while syncing one batch, you will have at least half of confirmed numbers in it, and tracker can process events from them as he syncs up with the tracked chain.
- `MaxBacklogSize` - should be configured in regards to the business logic for which you are using the tracker. If it is important to sync up and catch events from all confirmed missed blocks in the chain, then just leave this as 0, and tracker will sync up with every block you missed in the chain to get the desired events. If your node or application was down for a longer of period of time (days, months), and if it is not important to sync up all the events from missed blocks, configure MaxBacklogSize to be the number of latest blocks that you consider relevant for you to sync up until the latest chain block.
- `PollInterval` - should be configured to about the same as the block minting time on the tracked chain.
- `Logger` - you can pass your own logger here, as long as it implements the `Logger` interface from `go-hclog`.
- `Store` - you can pass your own store (as long as it implements the `EventTrackerStore` interface), or use the provided `BoltDBEventTrackerStore` from this repo, that creates and uses a `BoltDB` instance to store tracked blocks and events data.
- `BlockProvider` - it's basically a json rpc client connected to the provided public node (`RPCEndpoint`), used to poll block and event data from tracked blockchain.
- `EventSubscriber` - here you plugin your custom code for handling tracked events.
- `LogFilter` - here you configure which events (logs) on which contracts are going to be tracked. This is a map, where key is the contract address, and values are event signatures (event signatures are just hashed signatures of events, for example, if we have an event like this:
    ```solidity
        event SomeEvent(uint256 indexed id, address indexed sender, address indexed receiver, bytes data);
    ```
    then, its signature is: `keccak256(abi.Encode("SomeEvent(uint256,address,address,bytes))"`
)

### Tracking events

Start the Event Tracker by calling the `Start` function:

```go
    tracker, err := NewEventTracker(&EventTrackerConfig{
        RPCEndpoints: "https://some-url.com",
        PollTime: 10*time.Second,
        SyncBatchSize: 10,
        ...
    })
    if err != nil {
        return err
    }

    if err = tracker.Start(); err != nil {
        return err
    }
```

Event Tracker will begin monitoring the specified contracts and events in real-time.
On each tracked configured event, this function will be called on `EventSubscriber` to handle the event:
```go
    tracker.config.EventSubscriber.AddLog(log)
```
and if a custom `Store` is not provided, the default `BoltDBEventTrackerStore` will save the event in a `boltDB` instance, so it can be queried later by your application.

## Contributing
We welcome contributions to Event Tracker! If you have ideas for improvements or find bugs, please open an issue or submit a pull request.

## License
Event Tracker is licensed under the MIT License.