package escrow

import (
	"fmt"

	"github.com/arkade-os/arkd/pkg/ark-lib/script"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// SignForLeaf creates a Schnorr signature for a specific tapleaf of the escrow VTXO.
// It rebuilds the taproot tree from the script closures to compute the correct sighash.
func SignForLeaf(
	deal *Deal,
	leafIndex int,
	tx *wire.MsgTx,
	inputIndex int,
	allPrevOutputs []*wire.TxOut,
) ([]byte, error) {
	if deal.EscrowPriv == nil {
		return nil, fmt.Errorf("escrow private key not available")
	}
	if leafIndex < 0 || leafIndex >= len(deal.Script.Closures) {
		return nil, fmt.Errorf("invalid leaf index %d", leafIndex)
	}

	// Get the leaf script bytes
	leafScript, err := deal.Script.Closures[leafIndex].Script()
	if err != nil {
		return nil, fmt.Errorf("failed to get leaf script: %w", err)
	}

	// Rebuild the full tapscript tree to compute merkle proofs
	leaves := make([]txscript.TapLeaf, len(deal.Script.Closures))
	for i, c := range deal.Script.Closures {
		s, err := c.Script()
		if err != nil {
			return nil, fmt.Errorf("failed to get script for closure %d: %w", i, err)
		}
		leaves[i] = txscript.NewBaseTapLeaf(s)
	}

	tapLeaf := txscript.NewBaseTapLeaf(leafScript)

	// Build prev output fetcher for sighash computation
	fetcher := txscript.NewMultiPrevOutFetcher(buildPrevOutMap(tx, allPrevOutputs))

	sigHashes := txscript.NewTxSigHashes(tx, fetcher)

	sigHash, err := txscript.CalcTapscriptSignaturehash(
		sigHashes,
		txscript.SigHashDefault,
		tx,
		inputIndex,
		fetcher,
		tapLeaf,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to compute sighash: %w", err)
	}

	sig, err := schnorr.Sign(deal.EscrowPriv, sigHash)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	return sig.Serialize(), nil
}

// GetLeafClosure returns the closure at the given leaf index.
func GetLeafClosure(deal *Deal, leafIndex int) (script.Closure, error) {
	if leafIndex < 0 || leafIndex >= len(deal.Script.Closures) {
		return nil, fmt.Errorf("invalid leaf index %d", leafIndex)
	}
	return deal.Script.Closures[leafIndex], nil
}

func buildPrevOutMap(tx *wire.MsgTx, prevOutputs []*wire.TxOut) map[wire.OutPoint]*wire.TxOut {
	m := make(map[wire.OutPoint]*wire.TxOut)
	for i, input := range tx.TxIn {
		if i < len(prevOutputs) {
			m[input.PreviousOutPoint] = prevOutputs[i]
		}
	}
	return m
}
