package swap

import (
	"context"
	"encoding/hex"
	"fmt"
)

// PayoutConfig holds the configuration for an atomic payout (escrow VTXO → LN).
type PayoutConfig struct {
	LND       *LNDClient
	Elementsd *ElementsdClient

	// The LN invoice to pay the claimant (amount is encoded in the invoice)
	PaymentRequest string
	// Timeout in seconds for the LN payment
	TimeoutSecs int32
}

// PayoutResult contains the result of a successful payout.
type PayoutResult struct {
	// The preimage revealed by the payee
	Preimage []byte
	// The payment hash
	PaymentHash []byte
}

// Payout executes the atomic swap: escrow VTXO → LN payment.
//
// Protocol:
// 1. Claimant provides LN invoice
// 2. Service pays claimant's LN invoice from LND
// 3. Claimant's node settles → reveals preimage
// 4. Service uses preimage (returned here for HTLC claim if needed)
//
// The escrow VTXO claim (using release/timeout/dispute witness) is done
// separately by the caller after this function returns the preimage.
func Payout(ctx context.Context, cfg PayoutConfig) (*PayoutResult, error) {
	if cfg.LND == nil {
		return nil, fmt.Errorf("LND client is required")
	}
	if cfg.PaymentRequest == "" {
		return nil, fmt.Errorf("payment request is required")
	}
	if cfg.TimeoutSecs == 0 {
		cfg.TimeoutSecs = 60
	}

	// Pay the invoice — this blocks until settled or failed
	resp, err := cfg.LND.PayInvoice(ctx, cfg.PaymentRequest, cfg.TimeoutSecs)
	if err != nil {
		return nil, fmt.Errorf("failed to pay invoice: %w", err)
	}

	if resp.Status != "SUCCEEDED" {
		return nil, fmt.Errorf("payment not succeeded, status: %s", resp.Status)
	}

	preimage, err := hex.DecodeString(resp.PaymentPreimage)
	if err != nil {
		return nil, fmt.Errorf("failed to decode preimage: %w", err)
	}

	paymentHash, err := hex.DecodeString(resp.PaymentHash)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payment hash: %w", err)
	}

	return &PayoutResult{
		Preimage:    preimage,
		PaymentHash: paymentHash,
	}, nil
}
