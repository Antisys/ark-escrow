package swap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LNDClient is a REST client for LND with HODL invoice support.
type LNDClient struct {
	baseURL    string
	macaroon   string
	httpClient *http.Client
}

// NewLNDClient creates an LND REST client.
// macaroonHex is the hex-encoded admin macaroon.
func NewLNDClient(baseURL, macaroonHex string) *LNDClient {
	return &LNDClient{
		baseURL:  baseURL,
		macaroon: macaroonHex,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// TODO: accept TLS cert path for production use.
				// LND uses self-signed certs, so we skip verification for regtest.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
	}
}

func (c *LNDClient) do(ctx context.Context, method, path string, body interface{}) (json.RawMessage, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Grpc-Metadata-macaroon", c.macaroon)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("LND error %d: %s", resp.StatusCode, string(respBytes))
	}

	return json.RawMessage(respBytes), nil
}

// AddHoldInvoiceRequest is the request to create a HODL invoice.
type AddHoldInvoiceRequest struct {
	Hash   string `json:"hash"`   // base64-encoded payment hash
	Value  int64  `json:"value"`  // amount in sats
	Memo   string `json:"memo"`
	Expiry int64  `json:"expiry"` // seconds
}

// AddHoldInvoiceResponse is the response from AddHoldInvoice.
type AddHoldInvoiceResponse struct {
	PaymentRequest string `json:"payment_request"`
}

// AddHoldInvoice creates a HODL invoice that holds payment until explicitly settled.
func (c *LNDClient) AddHoldInvoice(ctx context.Context, hash []byte, valueSats int64, memo string, expirySecs int64) (string, error) {
	req := AddHoldInvoiceRequest{
		Hash:   base64.StdEncoding.EncodeToString(hash),
		Value:  valueSats,
		Memo:   memo,
		Expiry: expirySecs,
	}

	resp, err := c.do(ctx, http.MethodPost, "/v2/invoices/hodl", req)
	if err != nil {
		return "", fmt.Errorf("failed to add hold invoice: %w", err)
	}

	var result AddHoldInvoiceResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("failed to parse hold invoice response: %w", err)
	}

	return result.PaymentRequest, nil
}

// SettleInvoice settles a held HODL invoice by revealing the preimage.
func (c *LNDClient) SettleInvoice(ctx context.Context, preimage []byte) error {
	req := map[string]string{
		"preimage": base64.StdEncoding.EncodeToString(preimage),
	}
	_, err := c.do(ctx, http.MethodPost, "/v2/invoices/settle", req)
	if err != nil {
		return fmt.Errorf("failed to settle invoice: %w", err)
	}
	return nil
}

// LookupInvoiceResponse is the response from LookupInvoice.
type LookupInvoiceResponse struct {
	State       string `json:"state"`
	AmtPaidSat  string `json:"amt_paid_sat"`
	PaymentHash string `json:"r_hash"` // base64
}

// LookupInvoice looks up an invoice by payment hash.
func (c *LNDClient) LookupInvoice(ctx context.Context, paymentHash []byte) (*LookupInvoiceResponse, error) {
	hashB64 := base64.URLEncoding.EncodeToString(paymentHash)
	resp, err := c.do(ctx, http.MethodGet, "/v2/invoices/lookup?payment_hash="+hashB64, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup invoice: %w", err)
	}

	var result LookupInvoiceResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse lookup response: %w", err)
	}

	return &result, nil
}

// PayInvoiceRequest for sending a payment.
type PayInvoiceRequest struct {
	PaymentRequest string `json:"payment_request"`
	TimeoutSeconds int32  `json:"timeout_seconds"`
}

// PayInvoiceResponse from sending a payment.
type PayInvoiceResponse struct {
	PaymentHash     string `json:"payment_hash"`
	PaymentPreimage string `json:"payment_preimage"`
	Status          string `json:"status"`
	FailureReason   string `json:"failure_reason"`
}

// PayInvoice sends a payment from LND to an invoice. Returns the preimage on success.
// LND's /v2/router/send returns a streaming response with multiple JSON objects.
func (c *LNDClient) PayInvoice(ctx context.Context, payreq string, timeoutSecs int32) (*PayInvoiceResponse, error) {
	req := PayInvoiceRequest{
		PaymentRequest: payreq,
		TimeoutSeconds: timeoutSecs,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/router/send", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Grpc-Metadata-macaroon", c.macaroon)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LND error %d: %s", resp.StatusCode, string(body))
	}

	// Read streaming response — each line is a JSON object wrapped in {"result": ...}
	var lastResult PayInvoiceResponse
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var wrapper struct {
			Result PayInvoiceResponse `json:"result"`
		}
		if err := decoder.Decode(&wrapper); err != nil {
			return nil, fmt.Errorf("failed to decode streaming response: %w", err)
		}
		lastResult = wrapper.Result
		if lastResult.Status == "SUCCEEDED" || lastResult.Status == "FAILED" {
			break
		}
	}

	switch lastResult.Status {
	case "SUCCEEDED":
		return &lastResult, nil
	case "FAILED":
		return nil, fmt.Errorf("payment failed: %s", lastResult.FailureReason)
	default:
		return nil, fmt.Errorf("payment status unknown: stream ended with status %q", lastResult.Status)
	}
}

// GetInfo returns basic LND node info.
func (c *LNDClient) GetInfo(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/v1/getinfo", nil)
}

