package swap

import (
	"context"
	"fmt"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/vulpemventures/go-elements/elementsutil"
	elementsNetwork "github.com/vulpemventures/go-elements/network"
	"github.com/vulpemventures/go-elements/psetv2"
	"github.com/vulpemventures/go-elements/taproot"
	"github.com/vulpemventures/go-elements/transaction"
)

var defaultSequence = uint32(transaction.DefaultSequence)

// buildPSET creates a 1-input, 2-output (value + fee) PSET with tapscript witness data.
func buildPSET(
	txid string, vout, sequence uint32,
	inputAmount, outputAmount, fee uint64,
	prevoutScript, destScript, leafScript, controlBlock []byte,
	net *elementsNetwork.Network,
) (*psetv2.Pset, *psetv2.Updater, error) {
	locktime := uint32(0)
	p, err := psetv2.New(
		[]psetv2.InputArgs{{Txid: txid, TxIndex: vout, Sequence: sequence}},
		[]psetv2.OutputArgs{
			{Asset: net.AssetID, Amount: outputAmount, Script: destScript},
			{Asset: net.AssetID, Amount: fee},
		},
		&locktime,
	)
	if err != nil {
		return nil, nil, err
	}

	updater, err := psetv2.NewUpdater(p)
	if err != nil {
		return nil, nil, err
	}

	asset, err := elementsutil.AssetHashToBytes(net.AssetID)
	if err != nil {
		return nil, nil, err
	}
	value, err := elementsutil.ValueToBytes(inputAmount)
	if err != nil {
		return nil, nil, err
	}
	if err := updater.AddInWitnessUtxo(0, transaction.NewTxOutput(asset, value, prevoutScript)); err != nil {
		return nil, nil, err
	}

	elemLeaf := taproot.NewBaseTapElementsLeaf(leafScript)
	cb, err := taproot.ParseControlBlock(controlBlock)
	if err != nil {
		return nil, nil, err
	}
	if err := updater.AddInTapLeafScript(0, psetv2.TapLeafScript{
		TapElementsLeaf: elemLeaf,
		ControlBlock:    *cb,
	}); err != nil {
		return nil, nil, err
	}

	if err := updater.AddInTapInternalKey(0, schnorr.SerializePubKey(script.UnspendableKey())); err != nil {
		return nil, nil, err
	}

	return p, updater, nil
}

// signTapscriptInput signs input 0 with the given keys using the Elements sighash.
func signTapscriptInput(p *psetv2.Pset, keys []*btcec.PrivateKey, net *elementsNetwork.Network) error {
	utx, err := p.UnsignedTx()
	if err != nil {
		return err
	}

	input := p.Inputs[0]
	prevoutsScripts := [][]byte{input.WitnessUtxo.Script}
	prevoutsAssets := [][]byte{input.WitnessUtxo.Asset}
	prevoutsValues := [][]byte{input.WitnessUtxo.Value}

	genesisHash, err := chainhash.NewHashFromStr(net.GenesisBlockHash)
	if err != nil {
		return err
	}

	tapLeaf := input.TapLeafScript[0]
	elemLeaf := taproot.NewBaseTapElementsLeaf(tapLeaf.Script)
	leafHash := elemLeaf.TapHash()

	sighash := utx.HashForWitnessV1(
		0, prevoutsScripts, prevoutsAssets, prevoutsValues,
		txscript.SigHashDefault, genesisHash, &leafHash, nil,
	)

	for _, key := range keys {
		if key == nil {
			continue
		}
		sig, err := schnorr.Sign(key, sighash[:])
		if err != nil {
			return fmt.Errorf("sign failed for key %x: %w", schnorr.SerializePubKey(key.PubKey()), err)
		}
		p.Inputs[0].TapScriptSig = append(p.Inputs[0].TapScriptSig, psetv2.TapScriptSig{
			PartialSig: psetv2.PartialSig{
				PubKey:    schnorr.SerializePubKey(key.PubKey()),
				Signature: sig.Serialize(),
			},
			LeafHash: leafHash[:],
		})
	}

	return nil
}

// extractAndBroadcast finalizes a PSET and broadcasts it.
func extractAndBroadcast(ctx context.Context, elementsd *ElementsdClient, p *psetv2.Pset) (string, error) {
	finalTx, err := psetv2.Extract(p)
	if err != nil {
		return "", fmt.Errorf("extract failed: %w", err)
	}
	txHex, err := finalTx.ToHex()
	if err != nil {
		return "", fmt.Errorf("serialize failed: %w", err)
	}
	return elementsd.SendRawTransaction(ctx, txHex)
}

// serializeWitness encodes a witness stack in the standard wire format.
func serializeWitness(items [][]byte) []byte {
	var buf []byte
	buf = append(buf, byte(len(items)))
	for _, item := range items {
		buf = append(buf, compactSizeEncode(uint64(len(item)))...)
		buf = append(buf, item...)
	}
	return buf
}

func compactSizeEncode(n uint64) []byte {
	switch {
	case n < 0xfd:
		return []byte{byte(n)}
	case n <= 0xffff:
		return []byte{0xfd, byte(n), byte(n >> 8)}
	case n <= 0xffffffff:
		return []byte{0xfe, byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24)}
	default:
		return []byte{0xff, byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24),
			byte(n >> 32), byte(n >> 40), byte(n >> 48), byte(n >> 56)}
	}
}
