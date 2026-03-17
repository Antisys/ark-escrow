package swap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	elementsNetwork "github.com/vulpemventures/go-elements/network"
)

// FundConfig holds the configuration for an atomic fund operation (LN → escrow VTXO).
type FundConfig struct {
	LND       *LNDClient
	Elementsd *ElementsdClient

	// Escrow destination address (taproot)
	EscrowAddress string
	// Service's public key (receiver side of the Liquid HTLC)
	ServicePubKey *btcec.PublicKey
	// Service's private key (for claiming the HTLC → escrow)
	ServicePrivKey *btcec.PrivateKey
	// Amount in satoshis
	AmountSats uint64
	// Invoice expiry in seconds
	InvoiceExpiry int64
	// HTLC timeout in blocks (for service to reclaim if swap fails)
	HTLCTimeout uint32
	// Network HRP for address encoding
	NetworkHRP string
	// Elements network (for tx construction)
	Network *elementsNetwork.Network
}

// FundResult contains the result of a successful fund operation.
type FundResult struct {
	// The HODL invoice payment request
	PaymentRequest string
	// The preimage used for the HODL invoice
	Preimage []byte
	// The payment hash
	PaymentHash []byte
	// The HTLC funding txid on Liquid
	HTLCTxID string
	// The HTLC vout
	HTLCVout uint32
	// The escrow funding txid on Liquid
	EscrowTxID string
	// The escrow funding vout on Liquid
	EscrowVout uint32
	// The Liquid HTLC script (for recovery)
	HTLC *HTLCScript
}

// Fund executes the atomic swap: LN payment → escrow VTXO.
//
// Protocol:
// 1. Service picks preimage P, hash H = SHA256(P)
// 2. Service locks L-BTC in Liquid HTLC: "reveal P before timeout → escrow; else service reclaims"
// 3. Service creates HODL invoice on LND with hash H (payment held, not settled)
// 4. Buyer pays HODL invoice from their LN wallet → LND holds HTLC
// 5. Service detects pending payment on LND
// 6. Service sends L-BTC from HTLC to escrow address (claiming with preimage)
// 7. Service settles HODL invoice (reveals P, receives BTC)
//
// If service crashes between 2-3: HTLC times out, service reclaims L-BTC.
// If service crashes between 6-7: escrow VTXO exists, HODL invoice times out,
// service eats the cost but can detect on restart.
func Fund(ctx context.Context, cfg FundConfig) (*FundResult, error) {
	if cfg.LND == nil || cfg.Elementsd == nil {
		return nil, fmt.Errorf("LND and Elementsd clients are required")
	}
	if cfg.EscrowAddress == "" {
		return nil, fmt.Errorf("escrow address is required")
	}
	if cfg.AmountSats == 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if cfg.InvoiceExpiry == 0 {
		cfg.InvoiceExpiry = 3600 // 1 hour default
	}
	if cfg.HTLCTimeout == 0 {
		cfg.HTLCTimeout = HTLCDefaultTimeout
	}
	if cfg.NetworkHRP == "" {
		cfg.NetworkHRP = "ert"
	}
	if cfg.Network == nil {
		cfg.Network = &elementsNetwork.Regtest
	}

	// Step 1: Generate preimage and hash
	preimage := make([]byte, 32)
	if _, err := rand.Read(preimage); err != nil {
		return nil, fmt.Errorf("failed to generate preimage: %w", err)
	}
	hash := sha256.Sum256(preimage)

	// If service keys are provided, use the full HTLC intermediary flow.
	// Otherwise, fall back to direct funding (simplified PoC mode).
	if cfg.ServicePubKey != nil && cfg.ServicePrivKey != nil {
		return fundWithHTLC(ctx, cfg, preimage, hash)
	}
	return fundDirect(ctx, cfg, preimage, hash)
}

// fundWithHTLC implements the full atomic swap with a Liquid HTLC intermediary.
func fundWithHTLC(ctx context.Context, cfg FundConfig, preimage []byte, hash [32]byte) (*FundResult, error) {
	// Step 2: Lock L-BTC in Liquid HTLC
	// The HTLC pays to a tapscript: claim with preimage → escrow address, or refund after timeout → service.
	// For the HTLC, the "receiver" is a temporary key used to claim into the escrow,
	// and the "sender" is the service (who can reclaim after timeout).
	htlcResult, err := CreateAndFundHTLC(
		ctx,
		cfg.Elementsd,
		cfg.ServicePubKey,  // receiver: service claims with preimage to forward to escrow
		cfg.ServicePubKey,  // sender: service reclaims on timeout
		hash,
		cfg.AmountSats,
		cfg.HTLCTimeout,
		cfg.NetworkHRP,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Liquid HTLC: %w", err)
	}

	// Mine a block to confirm the HTLC (regtest only — on mainnet Liquid, blocks come every ~1min)
	addr, err := cfg.Elementsd.GetNewAddress(ctx)
	if err != nil {
		return nil, fmt.Errorf("HTLC funded (tx %s) but failed to get mining address: %w", htlcResult.TxID, err)
	}
	if _, err := cfg.Elementsd.GenerateToAddress(ctx, 1, addr); err != nil {
		return nil, fmt.Errorf("HTLC funded (tx %s) but failed to mine confirmation block: %w", htlcResult.TxID, err)
	}

	// Step 3: Create HODL invoice on LND
	memo := fmt.Sprintf("escrow-fund-%s", hex.EncodeToString(hash[:8]))
	payreq, err := cfg.LND.AddHoldInvoice(ctx, hash[:], int64(cfg.AmountSats), memo, cfg.InvoiceExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to create HODL invoice (HTLC funded in tx %s): %w",
			htlcResult.TxID, err)
	}

	return &FundResult{
		PaymentRequest: payreq,
		Preimage:       preimage,
		PaymentHash:    hash[:],
		HTLCTxID:       htlcResult.TxID,
		HTLCVout:       htlcResult.Vout,
		HTLC:           htlcResult.HTLC,
	}, nil
}

// ClaimHTLCToEscrow claims the Liquid HTLC using the preimage and sends funds to the escrow address.
// This should be called after the HODL invoice payment is detected (ACCEPTED state).
func ClaimHTLCToEscrow(
	ctx context.Context,
	elementsd *ElementsdClient,
	htlc *HTLCScript,
	htlcTxID string,
	htlcVout uint32,
	amount uint64,
	preimage []byte,
	signingKey *btcec.PrivateKey,
	escrowAddress string,
	net *elementsNetwork.Network,
) (string, error) {
	if net == nil {
		net = &elementsNetwork.Regtest
	}

	txid, err := SpendHTLC(ctx, elementsd, SpendHTLCConfig{
		HTLC:        htlc,
		FundTxID:    htlcTxID,
		FundVout:    htlcVout,
		Amount:      amount,
		LeafIndex:   0, // claim leaf
		SigningKey:   signingKey,
		DestAddress: escrowAddress,
		Preimage:    preimage,
		Network:     net,
	})
	if err != nil {
		return "", fmt.Errorf("failed to claim HTLC to escrow: %w", err)
	}

	return txid, nil
}

// fundDirect implements the simplified regtest flow: send L-BTC directly to escrow address.
// WARNING: This is NOT atomic — the service fronts L-BTC before the buyer pays the LN invoice.
// Use fundWithHTLC for production (requires ServicePubKey and ServicePrivKey).
func fundDirect(ctx context.Context, cfg FundConfig, preimage []byte, hash [32]byte) (*FundResult, error) {
	// Step 2: Send L-BTC directly to escrow address
	fundTxID, err := cfg.Elementsd.SendToAddress(ctx, cfg.EscrowAddress, cfg.AmountSats)
	if err != nil {
		return nil, fmt.Errorf("failed to fund escrow address: %w", err)
	}

	// Find the actual vout (sendtoaddress does not guarantee vout=0)
	vout, err := FindVoutByAddress(ctx, cfg.Elementsd, fundTxID, cfg.EscrowAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to find escrow vout in tx %s: %w", fundTxID, err)
	}

	// Step 3: Create HODL invoice on LND
	memo := fmt.Sprintf("escrow-fund-%s", hex.EncodeToString(hash[:8]))
	payreq, err := cfg.LND.AddHoldInvoice(ctx, hash[:], int64(cfg.AmountSats), memo, cfg.InvoiceExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to create HODL invoice (L-BTC sent to %s in tx %s): %w",
			cfg.EscrowAddress, fundTxID, err)
	}

	return &FundResult{
		PaymentRequest: payreq,
		Preimage:       preimage,
		PaymentHash:    hash[:],
		EscrowTxID:     fundTxID,
		EscrowVout:     vout,
	}, nil
}

// WaitForPaymentAndSettle waits for the HODL invoice to be held, then settles it.
// This should be called after the buyer pays the invoice.
func WaitForPaymentAndSettle(ctx context.Context, lnd *LNDClient, paymentHash, preimage []byte, pollInterval time.Duration) error {
	if pollInterval == 0 {
		pollInterval = 2 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		inv, err := lnd.LookupInvoice(ctx, paymentHash)
		if err != nil {
			return fmt.Errorf("failed to lookup invoice: %w", err)
		}

		switch inv.State {
		case "ACCEPTED":
			// Payment is held — settle it
			if err := lnd.SettleInvoice(ctx, preimage); err != nil {
				return fmt.Errorf("failed to settle invoice: %w", err)
			}
			return nil
		case "SETTLED":
			// Already settled
			return nil
		case "CANCELED":
			return fmt.Errorf("invoice was canceled")
		case "OPEN":
			// Still waiting for payment
			time.Sleep(pollInterval)
			continue
		default:
			return fmt.Errorf("unexpected invoice state: %s", inv.State)
		}
	}
}

// WaitForPaymentClaimAndSettle is the full HTLC flow: wait for payment, claim HTLC to escrow, settle invoice.
func WaitForPaymentClaimAndSettle(
	ctx context.Context,
	lnd *LNDClient,
	elementsd *ElementsdClient,
	fundResult *FundResult,
	signingKey *btcec.PrivateKey,
	escrowAddress string,
	amount uint64,
	net *elementsNetwork.Network,
	pollInterval time.Duration,
) (escrowTxID string, err error) {
	if pollInterval == 0 {
		pollInterval = 2 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		inv, err := lnd.LookupInvoice(ctx, fundResult.PaymentHash)
		if err != nil {
			return "", fmt.Errorf("failed to lookup invoice: %w", err)
		}

		switch inv.State {
		case "ACCEPTED":
			// Step 6: Claim HTLC → escrow address
			escrowTxID, err = ClaimHTLCToEscrow(
				ctx, elementsd, fundResult.HTLC,
				fundResult.HTLCTxID, fundResult.HTLCVout,
				amount, fundResult.Preimage, signingKey,
				escrowAddress, net,
			)
			if err != nil {
				return "", fmt.Errorf("failed to claim HTLC to escrow: %w", err)
			}

			// Step 7: Settle HODL invoice
			if err := lnd.SettleInvoice(ctx, fundResult.Preimage); err != nil {
				return escrowTxID, fmt.Errorf("HTLC claimed (tx %s) but failed to settle invoice: %w",
					escrowTxID, err)
			}
			return escrowTxID, nil

		case "SETTLED":
			return "", fmt.Errorf("invoice already settled without HTLC claim")
		case "CANCELED":
			return "", fmt.Errorf("invoice was canceled")
		case "OPEN":
			time.Sleep(pollInterval)
			continue
		default:
			return "", fmt.Errorf("unexpected invoice state: %s", inv.State)
		}
	}
}
