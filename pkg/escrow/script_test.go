package escrow

import (
	"crypto/rand"
	"testing"

	arklib "github.com/arkade-os/arkd/pkg/ark-lib"
	"github.com/arkade-os/arkd/pkg/ark-lib/script"
	"github.com/btcsuite/btcd/btcec/v2"
)

func generateKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestNewEscrowVtxoScript(t *testing.T) {
	buyerKey := generateKey(t).PubKey()
	sellerKey := generateKey(t).PubKey()
	escrowKey := generateKey(t).PubKey()
	serverKey := generateKey(t).PubKey()

	exitDelay := arklib.RelativeLocktime{
		Type:  arklib.LocktimeTypeBlock,
		Value: 144,
	}

	vtxoScript := NewEscrowVtxoScript(buyerKey, sellerKey, escrowKey, serverKey, exitDelay)

	// Must have 4 closures
	if len(vtxoScript.Closures) != 4 {
		t.Fatalf("expected 4 closures, got %d", len(vtxoScript.Closures))
	}

	// Leaf 0-2: MultisigClosure with 3 keys each
	for i := 0; i < 3; i++ {
		ms, ok := vtxoScript.Closures[i].(*script.MultisigClosure)
		if !ok {
			t.Fatalf("leaf %d: expected MultisigClosure, got %T", i, vtxoScript.Closures[i])
		}
		if len(ms.PubKeys) != 3 {
			t.Fatalf("leaf %d: expected 3 keys, got %d", i, len(ms.PubKeys))
		}
	}

	// Leaf 3: CSVMultisigClosure with 1 key
	csv, ok := vtxoScript.Closures[3].(*script.CSVMultisigClosure)
	if !ok {
		t.Fatalf("leaf 3: expected CSVMultisigClosure, got %T", vtxoScript.Closures[3])
	}
	if len(csv.PubKeys) != 1 {
		t.Fatalf("leaf 3: expected 1 key, got %d", len(csv.PubKeys))
	}
	if csv.Locktime.Value != 144 {
		t.Fatalf("leaf 3: expected locktime 144, got %d", csv.Locktime.Value)
	}
}

func TestEscrowAddress(t *testing.T) {
	buyerKey := generateKey(t).PubKey()
	sellerKey := generateKey(t).PubKey()
	escrowKey := generateKey(t).PubKey()
	serverKey := generateKey(t).PubKey()

	exitDelay := arklib.RelativeLocktime{
		Type:  arklib.LocktimeTypeBlock,
		Value: 144,
	}

	vtxoScript := NewEscrowVtxoScript(buyerKey, sellerKey, escrowKey, serverKey, exitDelay)

	addr, err := EscrowAddress(vtxoScript)
	if err != nil {
		t.Fatal(err)
	}

	// P2TR script: OP_1 (0x51) + OP_PUSHBYTES_32 (0x20) + 32 bytes = 34 bytes = 68 hex chars
	if len(addr) != 68 {
		t.Fatalf("expected 68 hex chars, got %d: %s", len(addr), addr)
	}

	// Must start with 5120 (OP_1 OP_PUSHBYTES_32)
	if addr[:4] != "5120" {
		t.Fatalf("expected P2TR prefix 5120, got %s", addr[:4])
	}
}

func TestEscrowAddressDeterministic(t *testing.T) {
	buyerKey := generateKey(t).PubKey()
	sellerKey := generateKey(t).PubKey()
	escrowKey := generateKey(t).PubKey()
	serverKey := generateKey(t).PubKey()

	exitDelay := arklib.RelativeLocktime{
		Type:  arklib.LocktimeTypeBlock,
		Value: 144,
	}

	addr1, err := EscrowAddress(NewEscrowVtxoScript(buyerKey, sellerKey, escrowKey, serverKey, exitDelay))
	if err != nil {
		t.Fatal(err)
	}

	addr2, err := EscrowAddress(NewEscrowVtxoScript(buyerKey, sellerKey, escrowKey, serverKey, exitDelay))
	if err != nil {
		t.Fatal(err)
	}

	if addr1 != addr2 {
		t.Fatalf("same keys should produce same address:\n  %s\n  %s", addr1, addr2)
	}
}

func TestEscrowAddressDifferentKeys(t *testing.T) {
	serverKey := generateKey(t).PubKey()
	exitDelay := arklib.RelativeLocktime{Type: arklib.LocktimeTypeBlock, Value: 144}

	addr1, _ := EscrowAddress(NewEscrowVtxoScript(
		generateKey(t).PubKey(), generateKey(t).PubKey(), generateKey(t).PubKey(), serverKey, exitDelay,
	))
	addr2, _ := EscrowAddress(NewEscrowVtxoScript(
		generateKey(t).PubKey(), generateKey(t).PubKey(), generateKey(t).PubKey(), serverKey, exitDelay,
	))

	if addr1 == addr2 {
		t.Fatal("different keys should produce different addresses")
	}
}

func TestValidateEscrowScript(t *testing.T) {
	buyerKey := generateKey(t).PubKey()
	sellerKey := generateKey(t).PubKey()
	escrowKey := generateKey(t).PubKey()
	serverKey := generateKey(t).PubKey()

	exitDelay := arklib.RelativeLocktime{Type: arklib.LocktimeTypeBlock, Value: 144}

	vtxoScript := NewEscrowVtxoScript(buyerKey, sellerKey, escrowKey, serverKey, exitDelay)

	if err := ValidateEscrowScript(vtxoScript, serverKey); err != nil {
		t.Fatalf("valid script should pass validation: %v", err)
	}
}

func TestValidateEscrowScriptMissingServer(t *testing.T) {
	buyerKey := generateKey(t).PubKey()
	sellerKey := generateKey(t).PubKey()
	escrowKey := generateKey(t).PubKey()
	serverKey := generateKey(t).PubKey()
	otherKey := generateKey(t).PubKey()

	exitDelay := arklib.RelativeLocktime{Type: arklib.LocktimeTypeBlock, Value: 144}

	// Build script with wrong server key in leaf 0
	vtxoScript := NewEscrowVtxoScript(buyerKey, sellerKey, escrowKey, otherKey, exitDelay)

	err := ValidateEscrowScript(vtxoScript, serverKey)
	if err == nil {
		t.Fatal("should fail when server key is missing")
	}
}

func TestEncodeDecodeTapscripts(t *testing.T) {
	buyerKey := generateKey(t).PubKey()
	sellerKey := generateKey(t).PubKey()
	escrowKey := generateKey(t).PubKey()
	serverKey := generateKey(t).PubKey()

	exitDelay := arklib.RelativeLocktime{Type: arklib.LocktimeTypeBlock, Value: 144}

	original := NewEscrowVtxoScript(buyerKey, sellerKey, escrowKey, serverKey, exitDelay)

	// Encode
	encoded, err := original.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if len(encoded) != 4 {
		t.Fatalf("expected 4 encoded scripts, got %d", len(encoded))
	}

	// Decode
	decoded := &script.TapscriptsVtxoScript{}
	if err := decoded.Decode(encoded); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if len(decoded.Closures) != 4 {
		t.Fatalf("decoded should have 4 closures, got %d", len(decoded.Closures))
	}

	// Addresses should match
	origAddr, _ := EscrowAddress(original)
	decodedAddr, _ := EscrowAddress(decoded)
	if origAddr != decodedAddr {
		t.Fatalf("roundtrip address mismatch:\n  orig:    %s\n  decoded: %s", origAddr, decodedAddr)
	}
}

// Suppress unused import warning for crypto/rand
var _ = rand.Reader
