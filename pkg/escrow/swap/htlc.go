package swap

import (
	"context"
	"encoding/json"
	"fmt"
)

// ElementsdClient wraps elementsd JSON-RPC for HTLC operations.
type ElementsdClient struct {
	rpc *elementsdRPC
}

// NewElementsdClient creates an elementsd client from a URL like http://admin1:123@localhost:18884.
func NewElementsdClient(elementsdURL string) (*ElementsdClient, error) {
	rpc, err := newElementsdRPC(elementsdURL)
	if err != nil {
		return nil, err
	}
	return &ElementsdClient{rpc: rpc}, nil
}

// HTLC represents a Liquid HTLC (hash-time-locked contract).
// Used for atomic swaps between LN and Liquid.
type HTLC struct {
	// The HTLC address (P2WSH on Liquid)
	Address string `json:"address"`
	// The redeem script hex
	RedeemScript string `json:"redeem_script"`
	// Funding txid after broadcast
	TxID string `json:"txid,omitempty"`
	// Funding vout
	Vout uint32 `json:"vout,omitempty"`
	// Amount in sats
	Amount uint64 `json:"amount"`
}

// SendToAddress sends L-BTC to an address. amountSats is the amount in satoshis. Returns txid.
func (c *ElementsdClient) SendToAddress(ctx context.Context, address string, amountSats uint64) (string, error) {
	// Format as string with 8 decimal places to avoid float64 precision loss.
	// elementsd accepts string amounts for exact decimal representation.
	amountStr := fmt.Sprintf("%d.%08d", amountSats/1e8, amountSats%uint64(1e8))
	result, err := c.rpc.call(ctx, "sendtoaddress", address, amountStr)
	if err != nil {
		return "", fmt.Errorf("sendtoaddress failed: %w", err)
	}
	var txid string
	if err := json.Unmarshal(result, &txid); err != nil {
		return "", fmt.Errorf("failed to parse txid: %w", err)
	}
	return txid, nil
}

// GetNewAddress returns a new bech32 address from the wallet.
func (c *ElementsdClient) GetNewAddress(ctx context.Context) (string, error) {
	result, err := c.rpc.call(ctx, "getnewaddress", "", "bech32")
	if err != nil {
		return "", fmt.Errorf("getnewaddress failed: %w", err)
	}
	var addr string
	if err := json.Unmarshal(result, &addr); err != nil {
		return "", fmt.Errorf("failed to parse address: %w", err)
	}
	return addr, nil
}

// GetBalance returns the wallet balance in BTC.
func (c *ElementsdClient) GetBalance(ctx context.Context) (float64, error) {
	result, err := c.rpc.call(ctx, "getbalance")
	if err != nil {
		return 0, fmt.Errorf("getbalance failed: %w", err)
	}

	var balances map[string]float64
	if err := json.Unmarshal(result, &balances); err != nil {
		// Try as single float
		var balance float64
		if err2 := json.Unmarshal(result, &balance); err2 != nil {
			return 0, fmt.Errorf("failed to parse balance: %w (raw: %s)", err, string(result))
		}
		return balance, nil
	}
	// Return bitcoin balance
	return balances["bitcoin"], nil
}

// GenerateToAddress mines blocks to an address. Returns block hashes.
func (c *ElementsdClient) GenerateToAddress(ctx context.Context, nblocks int, address string) ([]string, error) {
	result, err := c.rpc.call(ctx, "generatetoaddress", nblocks, address)
	if err != nil {
		return nil, fmt.Errorf("generatetoaddress failed: %w", err)
	}
	var hashes []string
	if err := json.Unmarshal(result, &hashes); err != nil {
		return nil, fmt.Errorf("failed to parse block hashes: %w", err)
	}
	return hashes, nil
}

// GetBlockCount returns the current block height.
func (c *ElementsdClient) GetBlockCount(ctx context.Context) (int64, error) {
	result, err := c.rpc.call(ctx, "getblockcount")
	if err != nil {
		return 0, fmt.Errorf("getblockcount failed: %w", err)
	}
	var count int64
	if err := json.Unmarshal(result, &count); err != nil {
		return 0, fmt.Errorf("failed to parse block count: %w", err)
	}
	return count, nil
}

// GetRawTransaction returns a raw transaction.
func (c *ElementsdClient) GetRawTransaction(ctx context.Context, txid string, verbose bool) (json.RawMessage, error) {
	return c.rpc.call(ctx, "getrawtransaction", txid, verbose)
}

// SendRawTransaction broadcasts a raw transaction hex.
func (c *ElementsdClient) SendRawTransaction(ctx context.Context, txHex string) (string, error) {
	result, err := c.rpc.call(ctx, "sendrawtransaction", txHex)
	if err != nil {
		return "", fmt.Errorf("sendrawtransaction failed: %w", err)
	}
	var txid string
	if err := json.Unmarshal(result, &txid); err != nil {
		return "", fmt.Errorf("failed to parse txid: %w", err)
	}
	return txid, nil
}

// GetTxOut returns UTXO info, or nil if spent.
func (c *ElementsdClient) GetTxOut(ctx context.Context, txid string, vout uint32) (json.RawMessage, error) {
	result, err := c.rpc.call(ctx, "gettxout", txid, vout)
	if err != nil {
		return nil, fmt.Errorf("gettxout failed: %w", err)
	}
	if string(result) == "null" {
		return nil, nil
	}
	return result, nil
}
