package escrow

import (
	"github.com/Antisys/ark-escrow/internal/script"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

type DealState string

const (
	DealStateCreated  DealState = "CREATED"
	DealStateJoined   DealState = "JOINED"
	DealStateFunded   DealState = "FUNDED"
	DealStateShipped  DealState = "SHIPPED"
	DealStateReleased DealState = "RELEASED"
	DealStateRefunded DealState = "REFUNDED"
	DealStateDisputed DealState = "DISPUTED"
)

type Deal struct {
	ID        string    `json:"id"`
	State     DealState `json:"state"`
	Title     string    `json:"title"`
	Amount    uint64    `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	SellerPubKey  string `json:"seller_pubkey"`
	BuyerPubKey   string `json:"buyer_pubkey,omitempty"`
	OraclePubKey  string `json:"oracle_pubkey"`
	SecretHash    string `json:"secret_hash,omitempty"`
	TimeoutBlocks uint32 `json:"timeout_blocks"`
	EscrowAddress string `json:"escrow_address,omitempty"`

	FundTxID  string `json:"fund_txid,omitempty"`
	FundVout  uint32 `json:"fund_vout,omitempty"`
	ClaimTxID string `json:"claim_txid,omitempty"`

	SellerPrivKey string `json:"seller_privkey,omitempty"`
	BuyerPrivKey  string `json:"buyer_privkey,omitempty"`
	Secret        string `json:"secret,omitempty"`
}

type JoinToken struct {
	DealID        string `json:"deal_id"`
	Title         string `json:"title"`
	Amount        uint64 `json:"amount"`
	SellerPubKey  string `json:"seller_pubkey"`
	OraclePubKey  string `json:"oracle_pubkey"`
	TimeoutBlocks uint32 `json:"timeout_blocks"`
}

func GenerateDealID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func NewDeal(title string, amount uint64, sellerPubKey, oraclePubKey string, timeoutBlocks uint32) (*Deal, error) {
	id, err := GenerateDealID()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	return &Deal{
		ID:            id,
		State:         DealStateCreated,
		Title:         title,
		Amount:        amount,
		SellerPubKey:  sellerPubKey,
		OraclePubKey:  oraclePubKey,
		TimeoutBlocks: timeoutBlocks,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func (d *Deal) JoinToken() JoinToken {
	return JoinToken{
		DealID:        d.ID,
		Title:         d.Title,
		Amount:        d.Amount,
		SellerPubKey:  d.SellerPubKey,
		OraclePubKey:  d.OraclePubKey,
		TimeoutBlocks: d.TimeoutBlocks,
	}
}

func (d *Deal) Join(buyerPubKey, secretHash string) error {
	if d.State != DealStateCreated {
		return fmt.Errorf("cannot join deal in state %s", d.State)
	}
	d.BuyerPubKey = buyerPubKey
	d.SecretHash = secretHash
	d.State = DealStateJoined
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func (d *Deal) SetEscrowAddress(addr string) {
	d.EscrowAddress = addr
	d.UpdatedAt = time.Now().UTC()
}

func (d *Deal) Fund(txid string, vout uint32) error {
	if d.State != DealStateJoined {
		return fmt.Errorf("cannot fund deal in state %s", d.State)
	}
	d.FundTxID = txid
	d.FundVout = vout
	d.State = DealStateFunded
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func (d *Deal) Ship() error {
	if d.State != DealStateFunded {
		return fmt.Errorf("cannot mark shipped in state %s", d.State)
	}
	d.State = DealStateShipped
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func (d *Deal) Release(claimTxID string) error {
	if d.State != DealStateFunded && d.State != DealStateShipped {
		return fmt.Errorf("cannot release deal in state %s", d.State)
	}
	d.ClaimTxID = claimTxID
	d.State = DealStateReleased
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func (d *Deal) Refund(claimTxID string) error {
	if d.State != DealStateFunded && d.State != DealStateShipped {
		return fmt.Errorf("cannot refund deal in state %s", d.State)
	}
	d.ClaimTxID = claimTxID
	d.State = DealStateRefunded
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func (d *Deal) Dispute(claimTxID string) error {
	if d.State != DealStateFunded && d.State != DealStateShipped {
		return fmt.Errorf("cannot dispute deal in state %s", d.State)
	}
	d.ClaimTxID = claimTxID
	d.State = DealStateDisputed
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func (d *Deal) EscrowParams() (*EscrowParams, error) {
	if d.SellerPubKey == "" || d.BuyerPubKey == "" || d.OraclePubKey == "" || d.SecretHash == "" {
		return nil, fmt.Errorf("deal is missing required keys or secret hash")
	}

	sellerPub, err := decodePubKey(d.SellerPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid seller pubkey: %w", err)
	}
	buyerPub, err := decodePubKey(d.BuyerPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid buyer pubkey: %w", err)
	}
	oraclePub, err := decodePubKey(d.OraclePubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid oracle pubkey: %w", err)
	}

	hashBytes, err := hex.DecodeString(d.SecretHash)
	if err != nil {
		return nil, fmt.Errorf("invalid secret hash: %w", err)
	}
	if len(hashBytes) != 32 {
		return nil, fmt.Errorf("secret hash must be 32 bytes, got %d", len(hashBytes))
	}

	var secretHash [32]byte
	copy(secretHash[:], hashBytes)

	return &EscrowParams{
		SellerPubKey: sellerPub,
		BuyerPubKey:  buyerPub,
		OraclePubKey: oraclePub,
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: d.TimeoutBlocks,
		},
	}, nil
}

func decodePubKey(hexStr string) (*btcec.PublicKey, error) {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, err
	}
	switch len(b) {
	case 32:
		return schnorr.ParsePubKey(b)
	case 33:
		return btcec.ParsePubKey(b)
	default:
		return nil, fmt.Errorf("unexpected pubkey length %d", len(b))
	}
}
