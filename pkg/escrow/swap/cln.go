package swap

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// CLNClient calls CLN via `docker exec` + `lightning-cli`.
type CLNClient struct {
	container string
	network   string
}

// NewCLNClient creates a CLN client that calls lightning-cli inside a Docker container.
// container is the Docker container name (e.g., "cln").
// network is the Lightning network (e.g., "regtest").
func NewCLNClient(container, network string) *CLNClient {
	if container == "" {
		container = "cln"
	}
	if network == "" {
		network = "regtest"
	}
	return &CLNClient{
		container: container,
		network:   network,
	}
}

func (c *CLNClient) call(ctx context.Context, method string, args ...string) (json.RawMessage, error) {
	cmdArgs := []string{"exec", c.container, "lightning-cli", "--network=" + c.network, method}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("lightning-cli %s failed: %w (output: %s)", method, err, strings.TrimSpace(string(output)))
	}

	return json.RawMessage(output), nil
}

// PayResponse is the result of paying an invoice.
type PayResponse struct {
	PaymentPreimage string `json:"payment_preimage"`
	PaymentHash     string `json:"payment_hash"`
	Status          string `json:"status"`
	AmountMsat      uint64 `json:"amount_msat"`
	AmountSentMsat  uint64 `json:"amount_sent_msat"`
}

// Pay pays a BOLT11 invoice from this CLN node.
func (c *CLNClient) Pay(ctx context.Context, bolt11 string) (*PayResponse, error) {
	resp, err := c.call(ctx, "pay", bolt11)
	if err != nil {
		return nil, fmt.Errorf("failed to pay invoice: %w", err)
	}

	var result PayResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse pay response: %w", err)
	}

	return &result, nil
}

// InvoiceResponse is the result of creating an invoice.
type InvoiceResponse struct {
	Bolt11      string `json:"bolt11"`
	PaymentHash string `json:"payment_hash"`
	ExpiresAt   int64  `json:"expires_at"`
}

// CreateInvoice creates a BOLT11 invoice on this CLN node.
func (c *CLNClient) CreateInvoice(ctx context.Context, amountMsat uint64, label, description string) (*InvoiceResponse, error) {
	resp, err := c.call(ctx, "invoice", fmt.Sprintf("%d", amountMsat), label, description)
	if err != nil {
		return nil, fmt.Errorf("failed to create invoice: %w", err)
	}

	var result InvoiceResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse invoice response: %w", err)
	}

	return &result, nil
}

// GetInfo returns basic CLN node info.
func (c *CLNClient) GetInfo(ctx context.Context) (json.RawMessage, error) {
	return c.call(ctx, "getinfo")
}

// ListFunds returns available funds.
func (c *CLNClient) ListFunds(ctx context.Context) (json.RawMessage, error) {
	return c.call(ctx, "listfunds")
}

// InvoiceStatus contains the status and amount of a CLN invoice.
type InvoiceStatus struct {
	Label          string `json:"label"`
	Status         string `json:"status"`
	AmountMsat     uint64 `json:"amount_msat"`
	AmountRecvMsat uint64 `json:"amount_received_msat"`
}

// WaitInvoice waits for an invoice (by label) to be paid and returns its status.
func (c *CLNClient) WaitInvoice(ctx context.Context, label string) (*InvoiceStatus, error) {
	resp, err := c.call(ctx, "waitinvoice", label)
	if err != nil {
		return nil, fmt.Errorf("waitinvoice failed: %w", err)
	}
	var result InvoiceStatus
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse waitinvoice response: %w", err)
	}
	return &result, nil
}
