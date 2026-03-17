package escrow

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil/base58"
)

const recoveryHRP = "arkescrow"

// RecoveryKit contains everything a user needs to claim their escrowed funds
// without the service. It is self-contained — no service state needed.
type RecoveryKit struct {
	// Role: "buyer" or "seller"
	Role string `json:"role"`
	// The user's private key (hex, 32 bytes)
	PrivKey string `json:"privkey"`
	// The buyer's escrow secret (hex, 32 bytes) — only present for buyer
	Secret string `json:"secret,omitempty"`

	// Escrow parameters (needed to reconstruct the tapscript tree)
	SellerPubKey  string `json:"seller_pubkey"`
	BuyerPubKey   string `json:"buyer_pubkey"`
	OraclePubKey  string `json:"oracle_pubkey"`
	SecretHash    string `json:"secret_hash"`
	TimeoutBlocks uint32 `json:"timeout_blocks"`

	// Funding outpoint (set after funding)
	FundTxID string `json:"fund_txid,omitempty"`
	FundVout uint32 `json:"fund_vout,omitempty"`
	Amount   uint64 `json:"amount"`

	// Network
	NetworkHRP string `json:"network_hrp"`
}

// RecoveryKitForBuyer creates a recovery kit for the buyer from a deal.
func RecoveryKitForBuyer(deal *Deal) (*RecoveryKit, error) {
	if deal.BuyerPrivKey == "" {
		return nil, fmt.Errorf("buyer private key not available in deal")
	}
	return &RecoveryKit{
		Role:          "buyer",
		PrivKey:       deal.BuyerPrivKey,
		Secret:        deal.Secret,
		SellerPubKey:  deal.SellerPubKey,
		BuyerPubKey:   deal.BuyerPubKey,
		OraclePubKey:  deal.OraclePubKey,
		SecretHash:    deal.SecretHash,
		TimeoutBlocks: deal.TimeoutBlocks,
		FundTxID:      deal.FundTxID,
		FundVout:      deal.FundVout,
		Amount:        deal.Amount,
		NetworkHRP:    "ert",
	}, nil
}

// RecoveryKitForSeller creates a recovery kit for the seller from a deal.
func RecoveryKitForSeller(deal *Deal) (*RecoveryKit, error) {
	if deal.SellerPrivKey == "" {
		return nil, fmt.Errorf("seller private key not available in deal")
	}
	return &RecoveryKit{
		Role:          "seller",
		PrivKey:       deal.SellerPrivKey,
		SellerPubKey:  deal.SellerPubKey,
		BuyerPubKey:   deal.BuyerPubKey,
		OraclePubKey:  deal.OraclePubKey,
		SecretHash:    deal.SecretHash,
		TimeoutBlocks: deal.TimeoutBlocks,
		FundTxID:      deal.FundTxID,
		FundVout:      deal.FundVout,
		Amount:        deal.Amount,
		NetworkHRP:    "ert",
	}, nil
}

// recoveryKitVersion is the base58check version byte for recovery kits.
const recoveryKitVersion = 0x42

// Encode serializes the recovery kit as a base58check-encoded string with HRP.
// The checksum protects against accidental corruption of the kit data.
func (r *RecoveryKit) Encode() (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("failed to marshal recovery kit: %w", err)
	}
	return recoveryHRP + base58.CheckEncode(data, recoveryKitVersion), nil
}

// DecodeRecoveryKit decodes a base58check-encoded recovery kit string.
func DecodeRecoveryKit(s string) (*RecoveryKit, error) {
	if len(s) <= len(recoveryHRP) || s[:len(recoveryHRP)] != recoveryHRP {
		return nil, fmt.Errorf("invalid recovery kit: missing %s prefix", recoveryHRP)
	}

	data, version, err := base58.CheckDecode(s[len(recoveryHRP):])
	if err != nil {
		return nil, fmt.Errorf("invalid recovery kit: %w", err)
	}
	if version != recoveryKitVersion {
		return nil, fmt.Errorf("invalid recovery kit: unexpected version %d", version)
	}

	var kit RecoveryKit
	if err := json.Unmarshal(data, &kit); err != nil {
		return nil, fmt.Errorf("invalid recovery kit: %w", err)
	}

	return &kit, nil
}

// JSON returns the recovery kit as pretty-printed JSON.
func (r *RecoveryKit) JSON() (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// EscrowParams reconstructs the escrow parameters from the recovery kit.
func (r *RecoveryKit) EscrowParams() (*EscrowParams, error) {
	deal := &Deal{
		SellerPubKey:  r.SellerPubKey,
		BuyerPubKey:   r.BuyerPubKey,
		OraclePubKey:  r.OraclePubKey,
		SecretHash:    r.SecretHash,
		TimeoutBlocks: r.TimeoutBlocks,
	}
	return deal.EscrowParams()
}

// EscrowAddress returns the escrow taproot address derived from the kit's parameters.
func (r *RecoveryKit) EscrowAddress() (string, error) {
	params, err := r.EscrowParams()
	if err != nil {
		return "", err
	}
	es, err := NewEscrowScript(*params)
	if err != nil {
		return "", err
	}
	hrp := r.NetworkHRP
	if hrp == "" {
		hrp = "ert"
	}
	return es.Address(hrp)
}

// Validate checks that the recovery kit has all required fields and that the
// private key corresponds to the role's public key.
func (r *RecoveryKit) Validate() error {
	if r.Role != "buyer" && r.Role != "seller" {
		return fmt.Errorf("invalid role: %s (must be 'buyer' or 'seller')", r.Role)
	}
	if r.PrivKey == "" {
		return fmt.Errorf("private key is required")
	}
	privBytes, err := hex.DecodeString(r.PrivKey)
	if err != nil || len(privBytes) != 32 {
		return fmt.Errorf("invalid private key: must be 32 bytes hex")
	}
	if r.SellerPubKey == "" || r.BuyerPubKey == "" || r.OraclePubKey == "" {
		return fmt.Errorf("all public keys are required")
	}
	if r.SecretHash == "" {
		return fmt.Errorf("secret hash is required")
	}
	if r.TimeoutBlocks == 0 {
		return fmt.Errorf("timeout blocks is required")
	}
	if r.Amount == 0 {
		return fmt.Errorf("amount is required")
	}

	// Verify the private key matches the role's public key (x-only comparison
	// since pubkeys are stored as 32-byte Schnorr keys without Y parity).
	_, pubFromPriv := btcec.PrivKeyFromBytes(privBytes)
	derivedX := schnorr.SerializePubKey(pubFromPriv)
	expectedPubHex := r.BuyerPubKey
	if r.Role == "seller" {
		expectedPubHex = r.SellerPubKey
	}
	expectedPubBytes, err := hex.DecodeString(expectedPubHex)
	if err != nil {
		return fmt.Errorf("invalid %s public key: %w", r.Role, err)
	}
	if len(expectedPubBytes) == 33 {
		expectedPubBytes = expectedPubBytes[1:] // strip compression prefix
	}
	if len(derivedX) != len(expectedPubBytes) {
		return fmt.Errorf("private key does not match %s public key", r.Role)
	}
	for i := range derivedX {
		if derivedX[i] != expectedPubBytes[i] {
			return fmt.Errorf("private key does not match %s public key", r.Role)
		}
	}

	return nil
}
