package swap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// elementsdRPC is a JSON-RPC client for elementsd.
type elementsdRPC struct {
	httpClient *http.Client
	url        string
	walletURL  string
	username   string
	password   string
	idCounter  atomic.Int64
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

func newElementsdRPC(elementsdURL string) (*elementsdRPC, error) {
	parsed, err := url.Parse(elementsdURL)
	if err != nil {
		return nil, fmt.Errorf("invalid elementsd URL: %w", err)
	}

	username := parsed.User.Username()
	password, _ := parsed.User.Password()

	baseURL := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	if parsed.Path != "" && parsed.Path != "/" {
		baseURL += parsed.Path
	}

	return &elementsdRPC{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		url:        baseURL,
		walletURL:  baseURL + "/wallet/ark",
		username:   username,
		password:   password,
	}, nil
}

func (c *elementsdRPC) call(ctx context.Context, method string, params ...interface{}) (json.RawMessage, error) {
	return c.callURL(ctx, c.walletURL, method, params...)
}

func (c *elementsdRPC) callBase(ctx context.Context, method string, params ...interface{}) (json.RawMessage, error) {
	return c.callURL(ctx, c.url, method, params...)
}

func (c *elementsdRPC) callURL(ctx context.Context, rpcURL, method string, params ...interface{}) (json.RawMessage, error) {
	if params == nil {
		params = []interface{}{}
	}

	reqBody := rpcRequest{
		JSONRPC: "2.0",
		ID:      int(c.idCounter.Add(1)),
		Method:  method,
		Params:  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.username, c.password)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RPC request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read RPC response: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to parse RPC response: %w (body: %s)", err, string(respBytes))
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return rpcResp.Result, nil
}
