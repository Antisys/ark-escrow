package escrow

import (
	"time"

	"github.com/arkade-os/arkd/pkg/ark-lib/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
)

// DealStatus represents the lifecycle state of an escrow deal.
type DealStatus string

const (
	DealCreated  DealStatus = "created"  // Deal created, awaiting funding
	DealFunded   DealStatus = "funded"   // VTXO funded with escrow script
	DealReleased DealStatus = "released" // Escrow released funds to seller
	DealRefunded DealStatus = "refunded" // Escrow refunded funds to buyer
	DealExpired  DealStatus = "expired"  // Deal expired (VTXO expiry reached)
)

// Deal represents an escrow agreement between buyer and seller,
// mediated by the escrow agent.
type Deal struct {
	ID           string
	BuyerPubKey  *btcec.PublicKey
	SellerPubKey *btcec.PublicKey
	EscrowPubKey *btcec.PublicKey  // Public key (shared with parties)
	EscrowPriv   *btcec.PrivateKey // Private key (kept secret by agent)
	ServerPubKey *btcec.PublicKey
	Amount       uint64
	Status       DealStatus
	VtxoOutpoint *wire.OutPoint // Funded VTXO reference
	Script       *script.TapscriptsVtxoScript
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
