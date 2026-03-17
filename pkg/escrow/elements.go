package escrow

import (
	"fmt"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/vulpemventures/go-elements/taproot"
)

// ElementsTapTree computes the taproot output key and indexed tree using
// Elements-specific tagged hashes (TapLeaf/elements instead of TapLeaf).
func ElementsTapTree(closures []script.Closure) (*btcec.PublicKey, *taproot.IndexedElementsTapScriptTree, error) {
	leaves := make([]taproot.TapElementsLeaf, len(closures))
	for i, c := range closures {
		s, err := c.Script()
		if err != nil {
			return nil, nil, fmt.Errorf("closure %d: %w", i, err)
		}
		leaves[i] = taproot.NewBaseTapElementsLeaf(s)
	}

	tree := taproot.AssembleTaprootScriptTree(leaves...)
	root := tree.RootNode.TapHash()
	outputKey := taproot.ComputeTaprootOutputKey(script.UnspendableKey(), root[:])

	return outputKey, tree, nil
}

func ElementsLeafScript(closures []script.Closure, leafIndex int) ([]byte, []byte, error) {
	if leafIndex < 0 || leafIndex >= len(closures) {
		return nil, nil, fmt.Errorf("leaf index %d out of range (%d leaves)", leafIndex, len(closures))
	}

	scriptBytes, err := closures[leafIndex].Script()
	if err != nil {
		return nil, nil, err
	}

	_, tree, err := ElementsTapTree(closures)
	if err != nil {
		return nil, nil, err
	}

	leaf := taproot.NewBaseTapElementsLeaf(scriptBytes)
	leafHash := leaf.TapHash()

	idx, ok := tree.LeafProofIndex[leafHash]
	if !ok {
		return nil, nil, fmt.Errorf("leaf hash not found in tree")
	}

	proof := tree.LeafMerkleProofs[idx]
	cb := proof.ToControlBlock(script.UnspendableKey())
	controlBlock, err := cb.ToBytes()
	if err != nil {
		return nil, nil, err
	}

	return scriptBytes, controlBlock, nil
}

func ElementsAddress(closures []script.Closure, hrp string) (string, error) {
	key, _, err := ElementsTapTree(closures)
	if err != nil {
		return "", err
	}
	return EncodeBech32m(hrp, schnorr.SerializePubKey(key))
}

func ElementsP2TRScript(closures []script.Closure) ([]byte, error) {
	key, _, err := ElementsTapTree(closures)
	if err != nil {
		return nil, err
	}
	return script.P2TRScript(key)
}
