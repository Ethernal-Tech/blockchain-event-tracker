package tracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Ethernal-Tech/ethgo"
)

// defaultRPCTimeout is used when EventTrackerConfig.RPCTimeout is not set.
const defaultRPCTimeout = 30 * time.Second

// maxErrorBodyLength limits how much of an unexpected http response ends up in an error.
const maxErrorBodyLength = 256

// httpProvider is a json rpc client over http(s) for the calls the tracker makes.
// Unlike the ethgo client, every call is bounded by a timeout, so a node that accepts
// a request and never answers can not block the tracker forever.
type httpProvider struct {
	endpoint string
	client   *http.Client
	nextID   atomic.Uint64
}

var _ Provider = (*httpProvider)(nil)

func newHTTPProvider(endpoint string, timeout time.Duration) *httpProvider {
	return &httpProvider{
		endpoint: endpoint,
		client:   &http.Client{Timeout: timeout},
	}
}

// isHTTPEndpoint reports whether the endpoint is served over http(s), and not over ws or ipc.
func isHTTPEndpoint(endpoint string) bool {
	endpoint = strings.ToLower(endpoint)

	return strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://")
}

// GetBlockByNumber implements Provider. It returns a nil block, and no error,
// when the node answers with null.
func (p *httpProvider) GetBlockByNumber(i ethgo.BlockNumber, full bool) (*ethgo.Block, error) {
	var block *ethgo.Block
	if err := p.call("eth_getBlockByNumber", &block, i.String(), full); err != nil {
		return nil, err
	}

	return block, nil
}

// GetLogs implements Provider.
func (p *httpProvider) GetLogs(filter *ethgo.LogFilter) ([]*ethgo.Log, error) {
	var logs []*ethgo.Log
	if err := p.call("eth_getLogs", &logs, newLogFilterRequest(filter)); err != nil {
		return nil, err
	}

	return logs, nil
}

// ChainID implements Provider.
func (p *httpProvider) ChainID() (*big.Int, error) {
	var out string
	if err := p.call("eth_chainId", &out); err != nil {
		return nil, err
	}

	chainID, ok := new(big.Int).SetString(strings.TrimPrefix(out, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("eth_chainId: invalid chain id: %q", out)
	}

	return chainID, nil
}

// logFilterRequest is the eth_getLogs filter object. It sends every address as an array,
// while the ethgo encoder drops the addresses when there is more than one.
type logFilterRequest struct {
	Address   []ethgo.Address `json:"address,omitempty"`
	Topics    [][]*ethgo.Hash `json:"topics,omitempty"`
	BlockHash *ethgo.Hash     `json:"blockHash,omitempty"`
	FromBlock string          `json:"fromBlock,omitempty"`
	ToBlock   string          `json:"toBlock,omitempty"`
}

func newLogFilterRequest(filter *ethgo.LogFilter) logFilterRequest {
	request := logFilterRequest{
		Address:   filter.Address,
		Topics:    filter.Topics,
		BlockHash: filter.BlockHash,
	}

	if filter.From != nil {
		request.FromBlock = filter.From.String()
	}

	if filter.To != nil {
		request.ToBlock = filter.To.String()
	}

	return request
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      uint64        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("json rpc error %d: %s: %s", e.Code, e.Message, e.Data)
	}

	return fmt.Sprintf("json rpc error %d: %s", e.Code, e.Message)
}

// call sends one json rpc request and decodes its result into out. An error from the
// http client is wrapped, so a timeout still satisfies net.Error with Timeout() true.
func (p *httpProvider) call(method string, out interface{}, params ...interface{}) error {
	if params == nil {
		params = []interface{}{}
	}

	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      p.nextID.Add(1),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return fmt.Errorf("%s: could not encode request: %w", method, err)
	}

	request, err := http.NewRequest(http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: could not create request: %w", method, err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}

	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("%s: could not read response: %w", method, err)
	}

	if response.StatusCode != http.StatusOK {
		if len(data) > maxErrorBodyLength {
			data = data[:maxErrorBodyLength]
		}

		return fmt.Errorf("%s: unexpected http status %d: %s", method, response.StatusCode, data)
	}

	var decoded rpcResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("%s: could not decode response: %w", method, err)
	}

	if decoded.Error != nil {
		return fmt.Errorf("%s: %w", method, decoded.Error)
	}

	if len(decoded.Result) == 0 {
		return fmt.Errorf("%s: %w", method, errMissingResult)
	}

	if err := json.Unmarshal(decoded.Result, out); err != nil {
		return fmt.Errorf("%s: could not decode result: %w", method, err)
	}

	return nil
}

var errMissingResult = errors.New("response has neither a result nor an error")
