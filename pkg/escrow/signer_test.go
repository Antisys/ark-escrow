package escrow

import (
	"testing"

	arklib "github.com/arkade-os/arkd/pkg/ark-lib"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func buildTestDeal(t *testing.T) *Deal {
	t.Helper()
	buyerPriv := generateKey(t)
	sellerPriv := generateKey(t)
	escrowPriv := generateKey(t)
	serverPriv := generateKey(t)

	exitDelay := arklib.RelativeLocktime{Type: arklib.LocktimeTypeBlock, Value: 144}
	script := NewEscrowVtxoScript(
		buyerPriv.PubKey(), sellerPriv.PubKey(), escrowPriv.PubKey(), serverPriv.PubKey(), exitDelay,
	)

	return &Deal{
		ID:           "test-deal",
		BuyerPubKey:  buyerPriv.PubKey(),
		SellerPubKey: sellerPriv.PubKey(),
		EscrowPubKey: escrowPriv.PubKey(),
		EscrowPriv:   escrowPriv,
		ServerPubKey: serverPriv.PubKey(),
		Amount:       500000,
		Script:       script,
	}
}

func buildTestTx(t *testing.T, deal *Deal) (*wire.MsgTx, []*wire.TxOut) {
	t.Helper()

	taprootKey, _ := EscrowTaprootKey(deal.Script)
	pkScript, _ := payToTaprootScript(taprootKey)

	prevOut := &wire.TxOut{
		Value:    int64(deal.Amount),
		PkScript: pkScript,
	}

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{
			Hash:  chainhash.Hash{0x01},
			Index: 0,
		},
	})
	tx.AddTxOut(&wire.TxOut{
		Value:    int64(deal.Amount) - 300,
		PkScript: []byte{0x51, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
	})

	return tx, []*wire.TxOut{prevOut}
}

func TestSignForLeaf_Release(t *testing.T) {
	deal := buildTestDeal(t)
	tx, prevOuts := buildTestTx(t, deal)

	sig, err := SignForLeaf(deal, LeafEscrowRelease, tx, 0, prevOuts)
	if err != nil {
		t.Fatalf("signing leaf 1 (release): %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("expected 64-byte Schnorr signature, got %d", len(sig))
	}

	// Verify signature
	parsedSig, err := schnorr.ParseSignature(sig)
	if err != nil {
		t.Fatalf("invalid signature format: %v", err)
	}

	leafScript, _ := deal.Script.Closures[LeafEscrowRelease].Script()
	tapLeaf := txscript.NewBaseTapLeaf(leafScript)
	fetcher := txscript.NewMultiPrevOutFetcher(buildPrevOutMap(tx, prevOuts))
	sigHashes := txscript.NewTxSigHashes(tx, fetcher)
	sigHash, _ := txscript.CalcTapscriptSignaturehash(
		sigHashes, txscript.SigHashDefault, tx, 0, fetcher, tapLeaf,
	)

	if !parsedSig.Verify(sigHash, deal.EscrowPubKey) {
		t.Fatal("signature verification failed")
	}
}

func TestSignForLeaf_Refund(t *testing.T) {
	deal := buildTestDeal(t)
	tx, prevOuts := buildTestTx(t, deal)

	sig, err := SignForLeaf(deal, LeafEscrowRefund, tx, 0, prevOuts)
	if err != nil {
		t.Fatalf("signing leaf 2 (refund): %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("expected 64-byte Schnorr signature, got %d", len(sig))
	}

	// Verify
	parsedSig, _ := schnorr.ParseSignature(sig)
	leafScript, _ := deal.Script.Closures[LeafEscrowRefund].Script()
	tapLeaf := txscript.NewBaseTapLeaf(leafScript)
	fetcher := txscript.NewMultiPrevOutFetcher(buildPrevOutMap(tx, prevOuts))
	sigHashes := txscript.NewTxSigHashes(tx, fetcher)
	sigHash, _ := txscript.CalcTapscriptSignaturehash(
		sigHashes, txscript.SigHashDefault, tx, 0, fetcher, tapLeaf,
	)
	if !parsedSig.Verify(sigHash, deal.EscrowPubKey) {
		t.Fatal("refund signature verification failed")
	}
}

func TestSignForLeaf_MutualRelease(t *testing.T) {
	deal := buildTestDeal(t)
	tx, prevOuts := buildTestTx(t, deal)

	sig, err := SignForLeaf(deal, LeafMutualRelease, tx, 0, prevOuts)
	if err != nil {
		t.Fatalf("signing leaf 0 (mutual): %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("expected 64-byte signature, got %d", len(sig))
	}
}

func TestSignForLeaf_InvalidLeafIndex(t *testing.T) {
	deal := buildTestDeal(t)
	tx, prevOuts := buildTestTx(t, deal)

	_, err := SignForLeaf(deal, -1, tx, 0, prevOuts)
	if err == nil {
		t.Fatal("should reject negative leaf index")
	}

	_, err = SignForLeaf(deal, 4, tx, 0, prevOuts)
	if err == nil {
		t.Fatal("should reject out-of-range leaf index")
	}

	_, err = SignForLeaf(deal, 99, tx, 0, prevOuts)
	if err == nil {
		t.Fatal("should reject way out-of-range leaf index")
	}
}

func TestSignForLeaf_NilPrivateKey(t *testing.T) {
	deal := buildTestDeal(t)
	deal.EscrowPriv = nil
	tx, prevOuts := buildTestTx(t, deal)

	_, err := SignForLeaf(deal, LeafEscrowRelease, tx, 0, prevOuts)
	if err == nil {
		t.Fatal("should fail without private key")
	}
}

func TestGetLeafClosure(t *testing.T) {
	deal := buildTestDeal(t)

	for i := 0; i < 4; i++ {
		c, err := GetLeafClosure(deal, i)
		if err != nil {
			t.Fatalf("leaf %d: %v", i, err)
		}
		if c == nil {
			t.Fatalf("leaf %d: closure is nil", i)
		}
	}

	_, err := GetLeafClosure(deal, 4)
	if err == nil {
		t.Fatal("should fail for index 4")
	}

	_, err = GetLeafClosure(deal, -1)
	if err == nil {
		t.Fatal("should fail for index -1")
	}
}
