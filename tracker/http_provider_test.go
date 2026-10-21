package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ethernal-Tech/blockchain-event-tracker/store"
	"github.com/Ethernal-Tech/ethgo"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

const zeroHash = "0x0000000000000000000000000000000000000000000000000000000000000000"

// blockJSON is a block, as a node returns it without transactions.
func blockJSON(number uint64) string {
	return fmt.Sprintf(`{
		"number": "0x%x", "hash": "%s", "parentHash": "%s", "sha3Uncles": "%s",
		"transactionsRoot": "%s", "stateRoot": "%s", "receiptsRoot": "%s", "mixHash": "%s",
		"miner": "0x0000000000000000000000000000000000000000", "difficulty": "0x0",
		"gasLimit": "0x1c9c380", "gasUsed": "0x0", "timestamp": "0x6553f100",
		"extraData": "0x", "nonce": "0x0000000000000000", "baseFeePerGas": "0x7",
		"transactions": [], "uncles": []
	}`, number, zeroHash, zeroHash, zeroHash, zeroHash, zeroHash, zeroHash, zeroHash)
}

// rpcServer answers every json rpc request with the response the handler returns for it,
// and records the requests it received.
func rpcServer(t *testing.T, handler func(method string, params []json.RawMessage) string) (
	*httptest.Server, *[]rpcRequestRecord,
) {
	t.Helper()

	var requests []rpcRequestRecord

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var request rpcRequestRecord

		require.NoError(t, json.Unmarshal(body, &request))

		requests = append(requests, request)

		_, _ = w.Write([]byte(handler(request.Method, request.Params)))
	}))
	t.Cleanup(server.Close)

	return server, &requests
}

type rpcRequestRecord struct {
	JSONRPC string            `json:"jsonrpc"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

func TestHTTPProvider(t *testing.T) {
	t.Parallel()

	t.Run("GetBlockByNumber requests the tag and decodes the block", func(t *testing.T) {
		t.Parallel()

		server, requests := rpcServer(t, func(string, []json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":1,"result":` + blockJSON(105) + `}`
		})

		block, err := newHTTPProvider(server.URL, time.Second).GetBlockByNumber(ethgo.Finalized, false)
		require.NoError(t, err)
		require.NotNil(t, block)
		require.Equal(t, uint64(105), block.Number)

		require.Len(t, *requests, 1)
		require.Equal(t, "2.0", (*requests)[0].JSONRPC)
		require.Equal(t, "eth_getBlockByNumber", (*requests)[0].Method)
		require.Len(t, (*requests)[0].Params, 2)
		require.JSONEq(t, `"finalized"`, string((*requests)[0].Params[0]))
		require.JSONEq(t, `false`, string((*requests)[0].Params[1]))
	})

	t.Run("GetBlockByNumber returns a nil block for a null result", func(t *testing.T) {
		t.Parallel()

		server, _ := rpcServer(t, func(string, []json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":1,"result":null}`
		})

		block, err := newHTTPProvider(server.URL, time.Second).GetBlockByNumber(ethgo.Finalized, false)
		require.NoError(t, err)
		require.Nil(t, block)
	})

	t.Run("GetLogs sends every address and the block range, and decodes the logs", func(t *testing.T) {
		t.Parallel()

		log := store.CreateTestLogForStateSyncEvent(t, 101, 3)
		log.BlockHash = ethgo.Hash{1}
		log.TransactionHash = ethgo.Hash{2}

		// written by hand, since ethgo's Log.MarshalJSON races with other tests that marshal logs
		rawLog := fmt.Sprintf(`{
			"address": "%s", "topics": ["%s", "%s", "%s"], "data": "0x%x",
			"blockNumber": "0x65", "blockHash": "%s", "transactionHash": "%s",
			"transactionIndex": "0x0", "logIndex": "0x3", "removed": false
		}`, log.Address, log.Topics[0], log.Topics[1], log.Topics[2], log.Data, log.BlockHash, log.TransactionHash)

		server, requests := rpcServer(t, func(string, []json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":1,"result":[` + rawLog + `]}`
		})

		filter := &ethgo.LogFilter{Address: []ethgo.Address{{1}, {2}}}
		filter.SetFromUint64(101)
		filter.SetToUint64(120)

		logs, err := newHTTPProvider(server.URL, time.Second).GetLogs(filter)
		require.NoError(t, err)
		require.Len(t, logs, 1)
		require.Equal(t, log.BlockNumber, logs[0].BlockNumber)
		require.Equal(t, log.LogIndex, logs[0].LogIndex)
		require.Equal(t, log.BlockHash, logs[0].BlockHash)
		require.Equal(t, log.TransactionHash, logs[0].TransactionHash)
		require.Equal(t, log.Topics, logs[0].Topics)
		require.Equal(t, log.Data, logs[0].Data)

		require.Len(t, *requests, 1)
		require.Equal(t, "eth_getLogs", (*requests)[0].Method)
		require.Len(t, (*requests)[0].Params, 1)
		require.JSONEq(t, `{
			"address": ["0x0100000000000000000000000000000000000000", "0x0200000000000000000000000000000000000000"],
			"fromBlock": "0x65",
			"toBlock": "0x78"
		}`, string((*requests)[0].Params[0]))
	})

	t.Run("ChainID parses the hex chain id", func(t *testing.T) {
		t.Parallel()

		server, requests := rpcServer(t, func(string, []json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":1,"result":"0x2105"}`
		})

		chainID, err := newHTTPProvider(server.URL, time.Second).ChainID()
		require.NoError(t, err)
		require.Equal(t, int64(8453), chainID.Int64())
		require.Equal(t, "eth_chainId", (*requests)[0].Method)
		require.Empty(t, (*requests)[0].Params)
	})

	t.Run("ChainID rejects an invalid chain id", func(t *testing.T) {
		t.Parallel()

		server, _ := rpcServer(t, func(string, []json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":1,"result":"0xzz"}`
		})

		_, err := newHTTPProvider(server.URL, time.Second).ChainID()
		require.ErrorContains(t, err, "invalid chain id")
	})

	t.Run("returns the json rpc error", func(t *testing.T) {
		t.Parallel()

		server, _ := rpcServer(t, func(string, []json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"block range extends beyond current head block"}}`
		})

		_, err := newHTTPProvider(server.URL, time.Second).GetLogs(&ethgo.LogFilter{})
		require.ErrorContains(t, err, "json rpc error -32000: block range extends beyond current head block")

		var rpcErr *rpcError

		require.True(t, errors.As(err, &rpcErr))
		require.Equal(t, -32000, rpcErr.Code)
	})

	t.Run("returns an error for an unexpected http status", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
		}))
		t.Cleanup(server.Close)

		_, err := newHTTPProvider(server.URL, time.Second).ChainID()
		require.ErrorContains(t, err, "unexpected http status 429")
	})

	t.Run("returns an error for a response without a result", func(t *testing.T) {
		t.Parallel()

		server, _ := rpcServer(t, func(string, []json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":1}`
		})

		_, err := newHTTPProvider(server.URL, time.Second).ChainID()
		require.ErrorIs(t, err, errMissingResult)
	})

	t.Run("a node that never answers times out with a net timeout error", func(t *testing.T) {
		t.Parallel()

		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			<-release
		}))

		t.Cleanup(func() {
			close(release)
			server.Close()
		})

		started := time.Now()

		_, err := newHTTPProvider(server.URL, 200*time.Millisecond).GetBlockByNumber(ethgo.Latest, false)
		require.Error(t, err)
		require.Less(t, time.Since(started), 5*time.Second)

		var netErr net.Error

		require.True(t, errors.As(err, &netErr))
		require.True(t, netErr.Timeout())
	})
}

func TestSetupProvider_HTTPEndpointUsesHTTPProvider(t *testing.T) {
	t.Parallel()

	config := &EventTrackerConfig{RPCEndpoint: "https://some-rpc-url.com", RPCTimeout: time.Second}

	require.NoError(t, setupProvider(config, false))

	provider, ok := config.Provider.(*httpProvider)
	require.True(t, ok)
	require.Equal(t, time.Second, provider.client.Timeout)
	require.Nil(t, config.RPCClient)
}

// A node that accepts requests and never answers must not keep the tracker running after
// its context is cancelled. Before RPCTimeout, Start stayed in the rpc call forever.
func TestEventTracker_Start_StopsWhenNodeNeverAnswers(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	requestSeen := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var request rpcRequestRecord
		_ = json.Unmarshal(body, &request)

		if request.Method == "eth_chainId" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))

			return
		}

		select {
		case requestSeen <- struct{}{}:
		default:
		}

		<-release
	}))

	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	config := &EventTrackerConfig{
		RPCEndpoint:     server.URL,
		RPCTimeout:      300 * time.Millisecond,
		SyncBatchSize:   10,
		PollInterval:    time.Second,
		Logger:          hclog.NewNullLogger(),
		LogFilter:       map[ethgo.Address][]ethgo.Hash{ethgo.ZeroAddress: {store.StateSyncEventABI.ID()}},
		EventSubscriber: new(mockEventSubscriber),
	}

	eventTracker, err := NewEventTracker(config, store.NewTestTrackerStore(t))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		eventTracker.Start(ctx)
		close(stopped)
	}()

	select {
	case <-requestSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("tracker never sent the boundary request")
	}

	cancel()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("tracker is still running 5s after cancel, stuck in the rpc call")
	}
}
