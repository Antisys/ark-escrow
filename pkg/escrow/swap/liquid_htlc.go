package swap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript"
	elementsNetwork "github.com/vulpemventures/go-elements/network"
)

const (
	HTLCDefaultTimeout = 10
	HTLCEstimatedFee   = uint64(500)
	// dustLimit is the minimum output value in satoshis. Outputs below this
	// are rejected by Elements/Liquid nodes as non-standard ("dust").
	dustLimit = uint64(546)
)

// HTLCScript represents a Liquid HTLC as a 2-leaf taproot output.
type HTLCScript struct {
	ReceiverPubKey *btcec.PublicKey
	SenderPubKey   *btcec.PublicKey
	PaymentHash    [32]byte
	Timeout        script.RelativeLocktime
	Closures       []script.Closure
}

func NewHTLCScript(
	receiverPubKey, senderPubKey *btcec.PublicKey,
	paymentHash [32]byte,
	timeoutBlocks uint32,
) (*HTLCScript, error) {
	if receiverPubKey == nil || senderPubKey == nil {
		return nil, fmt.Errorf("both receiver and sender public keys are required")
	}

	condition, err := txscript.NewScriptBuilder().
		AddOp(txscript.OP_SHA256).
		AddData(paymentHash[:]).
		AddOp(txscript.OP_EQUAL).
		Script()
	if err != nil {
		return nil, fmt.Errorf("failed to build claim condition: %w", err)
	}

	timeout := script.RelativeLocktime{
		Type:  script.LocktimeTypeBlock,
		Value: timeoutBlocks,
	}

	closures := []script.Closure{
		&script.ConditionMultisigClosure{
			MultisigClosure: script.MultisigClosure{
				PubKeys: []*btcec.PublicKey{receiverPubKey},
				Type:    script.MultisigTypeChecksig,
			},
			Condition: condition,
		},
		&script.CSVMultisigClosure{
			MultisigClosure: script.MultisigClosure{
				PubKeys: []*btcec.PublicKey{senderPubKey},
				Type:    script.MultisigTypeChecksig,
			},
			Locktime: timeout,
		},
	}

	return &HTLCScript{
		ReceiverPubKey: receiverPubKey,
		SenderPubKey:   senderPubKey,
		PaymentHash:    paymentHash,
		Timeout:        timeout,
		Closures:       closures,
	}, nil
}

func (h *HTLCScript) ClaimClosure() *script.ConditionMultisigClosure {
	return h.Closures[0].(*script.ConditionMultisigClosure)
}

func (h *HTLCScript) RefundClosure() *script.CSVMultisigClosure {
	return h.Closures[1].(*script.CSVMultisigClosure)
}

func (h *HTLCScript) P2TRScript() ([]byte, error) {
	return escrow.ElementsP2TRScript(h.Closures)
}

func (h *HTLCScript) Address(hrp string) (string, error) {
	key, _, err := escrow.ElementsTapTree(h.Closures)
	if err != nil {
		return "", err
	}
	return escrow.EncodeBech32m(hrp, schnorr.SerializePubKey(key))
}

func (h *HTLCScript) LeafScript(leafIndex int) ([]byte, []byte, error) {
	return escrow.ElementsLeafScript(h.Closures, leafIndex)
}

type HTLCFundResult struct {
	HTLC       *HTLCScript
	TxID       string
	Vout       uint32
	Amount     uint64
	P2TRScript []byte
}

func CreateAndFundHTLC(
	ctx context.Context,
	elementsd *ElementsdClient,
	receiverPubKey, senderPubKey *btcec.PublicKey,
	paymentHash [32]byte,
	amountSats uint64,
	timeoutBlocks uint32,
	networkHRP string,
) (*HTLCFundResult, error) {
	htlc, err := NewHTLCScript(receiverPubKey, senderPubKey, paymentHash, timeoutBlocks)
	if err != nil {
		return nil, err
	}

	addr, err := htlc.Address(networkHRP)
	if err != nil {
		return nil, err
	}

	p2trScript, err := htlc.P2TRScript()
	if err != nil {
		return nil, err
	}

	txid, err := elementsd.SendToAddress(ctx, addr, amountSats)
	if err != nil {
		return nil, fmt.Errorf("failed to fund HTLC: %w", err)
	}

	// Find the actual vout (sendtoaddress does not guarantee vout=0)
	vout, err := FindVoutByAddress(ctx, elementsd, txid, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to find HTLC vout in tx %s: %w", txid, err)
	}

	return &HTLCFundResult{
		HTLC:       htlc,
		TxID:       txid,
		Vout:       vout,
		Amount:     amountSats,
		P2TRScript: p2trScript,
	}, nil
}

type SpendHTLCConfig struct {
	HTLC        *HTLCScript
	FundTxID    string
	FundVout    uint32
	Amount      uint64
	LeafIndex   int
	SigningKey   *btcec.PrivateKey
	DestAddress string
	Preimage    []byte
	Network     *elementsNetwork.Network
	Fee         uint64
}

func SpendHTLC(ctx context.Context, elementsd *ElementsdClient, cfg SpendHTLCConfig) (string, error) {
	if cfg.HTLC == nil || cfg.SigningKey == nil {
		return "", fmt.Errorf("HTLC and signing key are required")
	}
	if cfg.Network == nil {
		cfg.Network = &elementsNetwork.Regtest
	}
	if cfg.Fee == 0 {
		cfg.Fee = HTLCEstimatedFee
	}

	leafScript, controlBlock, err := cfg.HTLC.LeafScript(cfg.LeafIndex)
	if err != nil {
		return "", err
	}

	p2trScript, err := cfg.HTLC.P2TRScript()
	if err != nil {
		return "", err
	}

	destScript, err := escrow.AddressToOutputScript(cfg.DestAddress)
	if err != nil {
		return "", err
	}

	outputAmount := cfg.Amount - cfg.Fee
	if outputAmount <= dustLimit {
		return "", fmt.Errorf("output amount %d below dust after fee %d", outputAmount, cfg.Fee)
	}

	sequence := defaultSequence
	if cfg.LeafIndex == 1 {
		sequence, err = script.BIP68Sequence(cfg.HTLC.RefundClosure().Locktime)
		if err != nil {
			return "", err
		}
	}

	p, updater, err := buildPSET(cfg.FundTxID, cfg.FundVout, sequence, cfg.Amount, outputAmount, cfg.Fee, p2trScript, destScript, leafScript, controlBlock, cfg.Network)
	if err != nil {
		return "", err
	}
	_ = updater

	if err := signTapscriptInput(p, []*btcec.PrivateKey{cfg.SigningKey}, cfg.Network); err != nil {
		return "", err
	}

	// Build witness
	sig := p.Inputs[0].TapScriptSig[0].PartialSig.Signature
	if cfg.LeafIndex == 0 && cfg.Preimage != nil {
		p.Inputs[0].FinalScriptWitness = serializeWitness([][]byte{sig, cfg.Preimage, leafScript, controlBlock})
	} else {
		p.Inputs[0].FinalScriptWitness = serializeWitness([][]byte{sig, leafScript, controlBlock})
	}

	return extractAndBroadcast(ctx, elementsd, p)
}

// FindVoutByAddress searches a transaction's outputs for the one matching the given address.
func FindVoutByAddress(ctx context.Context, elementsd *ElementsdClient, txid string, address string) (uint32, error) {
	raw, err := elementsd.GetRawTransaction(ctx, txid, true)
	if err != nil {
		return 0, err
	}

	var tx struct {
		Vout []struct {
			N            uint32 `json:"n"`
			ScriptPubKey struct {
				Address string `json:"address"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return 0, fmt.Errorf("failed to parse transaction: %w", err)
	}

	for _, vout := range tx.Vout {
		if vout.ScriptPubKey.Address == address {
			return vout.N, nil
		}
	}

	return 0, fmt.Errorf("address %s not found in transaction %s", address, txid)
}
