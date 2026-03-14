package escrow

import (
	"encoding/hex"
	"fmt"

	arklib "github.com/arkade-os/arkd/pkg/ark-lib"
	"github.com/arkade-os/arkd/pkg/ark-lib/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript"
)

// NewEscrowVtxoScript creates a VTXO tapscript with 4 spending paths for escrow:
//
//	Leaf 0: Buyer + Seller + Server  — mutual release (happy path)
//	Leaf 1: Seller + Escrow + Server — escrow approves delivery → seller gets paid
//	Leaf 2: Buyer + Escrow + Server  — escrow approves refund → buyer gets refund
//	Leaf 3: Buyer after CSV delay    — buyer unilateral exit (trustless fallback)
//
// The server (ASP) co-signs every leaf except the timeout exit — this is inherent
// to Ark's design and ensures the VTXO tree remains valid.
func NewEscrowVtxoScript(
	buyerKey, sellerKey, escrowKey, serverKey *btcec.PublicKey,
	exitDelay arklib.RelativeLocktime,
) *script.TapscriptsVtxoScript {
	return &script.TapscriptsVtxoScript{
		Closures: []script.Closure{
			// Leaf 0: Mutual release — both buyer and seller agree
			&script.MultisigClosure{
				PubKeys: []*btcec.PublicKey{buyerKey, sellerKey, serverKey},
			},
			// Leaf 1: Escrow releases to seller
			&script.MultisigClosure{
				PubKeys: []*btcec.PublicKey{sellerKey, escrowKey, serverKey},
			},
			// Leaf 2: Escrow refunds to buyer
			&script.MultisigClosure{
				PubKeys: []*btcec.PublicKey{buyerKey, escrowKey, serverKey},
			},
			// Leaf 3: Buyer unilateral exit after timeout
			&script.CSVMultisigClosure{
				MultisigClosure: script.MultisigClosure{
					PubKeys: []*btcec.PublicKey{buyerKey},
				},
				Locktime: exitDelay,
			},
		},
	}
}

// EscrowAddress computes the P2TR address for an escrow VTXO script.
// Returns the hex-encoded scriptPubKey (OP_1 <32-byte-xonly-pubkey>).
func EscrowAddress(vtxoScript *script.TapscriptsVtxoScript) (string, error) {
	taprootKey, _, err := vtxoScript.TapTree()
	if err != nil {
		return "", fmt.Errorf("failed to build tap tree: %w", err)
	}

	pkScript, err := payToTaprootScript(taprootKey)
	if err != nil {
		return "", fmt.Errorf("failed to create P2TR script: %w", err)
	}

	return hex.EncodeToString(pkScript), nil
}

// EscrowTaprootKey returns the x-only public key for the escrow VTXO.
func EscrowTaprootKey(vtxoScript *script.TapscriptsVtxoScript) (*btcec.PublicKey, error) {
	taprootKey, _, err := vtxoScript.TapTree()
	if err != nil {
		return nil, fmt.Errorf("failed to build tap tree: %w", err)
	}
	return taprootKey, nil
}

// ValidateEscrowScript checks that a TapscriptsVtxoScript has the expected
// escrow structure: 3 forfeit closures (multisig) + 1 exit closure (CSV).
func ValidateEscrowScript(
	vtxoScript *script.TapscriptsVtxoScript,
	serverKey *btcec.PublicKey,
) error {
	if len(vtxoScript.Closures) != 4 {
		return fmt.Errorf("expected 4 closures, got %d", len(vtxoScript.Closures))
	}

	// First 3 must be MultisigClosure (forfeit paths)
	for i := 0; i < 3; i++ {
		ms, ok := vtxoScript.Closures[i].(*script.MultisigClosure)
		if !ok {
			return fmt.Errorf("closure %d: expected MultisigClosure, got %T", i, vtxoScript.Closures[i])
		}
		if len(ms.PubKeys) != 3 {
			return fmt.Errorf("closure %d: expected 3 pubkeys, got %d", i, len(ms.PubKeys))
		}
		if !containsPubKey(ms.PubKeys, serverKey) {
			return fmt.Errorf("closure %d: missing server pubkey", i)
		}
	}

	// Last must be CSVMultisigClosure (exit path)
	csv, ok := vtxoScript.Closures[3].(*script.CSVMultisigClosure)
	if !ok {
		return fmt.Errorf("closure 3: expected CSVMultisigClosure, got %T", vtxoScript.Closures[3])
	}
	if len(csv.PubKeys) != 1 {
		return fmt.Errorf("closure 3: expected 1 pubkey (buyer), got %d", len(csv.PubKeys))
	}

	return nil
}

// LeafIndex constants for referencing specific escrow spending paths.
const (
	LeafMutualRelease = 0 // Buyer + Seller + Server
	LeafEscrowRelease = 1 // Seller + Escrow + Server
	LeafEscrowRefund  = 2 // Buyer + Escrow + Server
	LeafBuyerExit     = 3 // Buyer after timeout
)

func payToTaprootScript(taprootKey *btcec.PublicKey) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddOp(txscript.OP_1).
		AddData(schnorr.SerializePubKey(taprootKey)).
		Script()
}

func containsPubKey(keys []*btcec.PublicKey, target *btcec.PublicKey) bool {
	targetBytes := schnorr.SerializePubKey(target)
	for _, k := range keys {
		if bytesEqual(schnorr.SerializePubKey(k), targetBytes) {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
