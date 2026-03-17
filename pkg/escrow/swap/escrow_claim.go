package swap

import (
	"github.com/Antisys/ark-escrow/internal/script"
	"context"
	"fmt"

	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	elementsNetwork "github.com/vulpemventures/go-elements/network"
	"github.com/vulpemventures/go-elements/psetv2"
)

type EscrowLeaf int

const (
	EscrowLeafRelease       EscrowLeaf = 0
	EscrowLeafTimeout       EscrowLeaf = 1
	EscrowLeafDisputeSeller EscrowLeaf = 2
	EscrowLeafDisputeBuyer  EscrowLeaf = 3
)

type ClaimEscrowConfig struct {
	EscrowScript *escrow.EscrowScript
	FundTxID     string
	FundVout     uint32
	Amount       uint64
	Leaf         EscrowLeaf
	SigningKeys  []*btcec.PrivateKey
	Preimage     []byte // required for Release leaf only
	DestAddress  string
	Fee          uint64
	Network      *elementsNetwork.Network
}

type ClaimEscrowResult struct {
	TxID  string
	TxHex string
}

func ClaimEscrow(ctx context.Context, elementsd *ElementsdClient, cfg ClaimEscrowConfig) (*ClaimEscrowResult, error) {
	if cfg.EscrowScript == nil {
		return nil, fmt.Errorf("escrow script is required")
	}
	if len(cfg.SigningKeys) == 0 {
		return nil, fmt.Errorf("at least one signing key is required")
	}
	if cfg.DestAddress == "" {
		return nil, fmt.Errorf("destination address is required")
	}
	if cfg.Network == nil {
		cfg.Network = &elementsNetwork.Regtest
	}
	if cfg.Fee == 0 {
		cfg.Fee = HTLCEstimatedFee
	}

	leafIndex := int(cfg.Leaf)
	if leafIndex < 0 || leafIndex >= len(cfg.EscrowScript.Closures) {
		return nil, fmt.Errorf("invalid leaf index %d", leafIndex)
	}

	if cfg.Amount <= cfg.Fee || cfg.Amount-cfg.Fee <= 546 {
		return nil, fmt.Errorf("output amount %d below dust after fee %d", cfg.Amount-cfg.Fee, cfg.Fee)
	}

	leafScriptBytes, controlBlock, err := escrow.ElementsLeafScript(cfg.EscrowScript.Closures, leafIndex)
	if err != nil {
		return nil, err
	}

	p2trScript, err := escrow.ElementsP2TRScript(cfg.EscrowScript.Closures)
	if err != nil {
		return nil, err
	}

	destScript, err := escrow.AddressToOutputScript(cfg.DestAddress)
	if err != nil {
		return nil, err
	}

	sequence := defaultSequence
	if cfg.Leaf == EscrowLeafTimeout {
		sequence, err = script.BIP68Sequence(cfg.EscrowScript.TimeoutClosure().Locktime)
		if err != nil {
			return nil, err
		}
	}

	outputAmount := cfg.Amount - cfg.Fee
	p, _, err := buildPSET(cfg.FundTxID, cfg.FundVout, sequence, cfg.Amount, outputAmount, cfg.Fee, p2trScript, destScript, leafScriptBytes, controlBlock, cfg.Network)
	if err != nil {
		return nil, err
	}

	if err := signTapscriptInput(p, cfg.SigningKeys, cfg.Network); err != nil {
		return nil, err
	}

	if err := finalizeEscrowWitness(p, cfg.Leaf, cfg.SigningKeys, cfg.Preimage, leafScriptBytes, controlBlock); err != nil {
		return nil, err
	}

	txid, err := extractAndBroadcast(ctx, elementsd, p)
	if err != nil {
		return nil, fmt.Errorf("failed to broadcast escrow claim: %w", err)
	}

	return &ClaimEscrowResult{TxID: txid}, nil
}

func finalizeEscrowWitness(
	p *psetv2.Pset,
	leaf EscrowLeaf,
	keys []*btcec.PrivateKey,
	preimage, leafScript, controlBlock []byte,
) error {
	sigs := make(map[string][]byte)
	for _, tapSig := range p.Inputs[0].TapScriptSig {
		sigs[fmt.Sprintf("%x", tapSig.PartialSig.PubKey)] = tapSig.PartialSig.Signature
	}

	getSig := func(key *btcec.PrivateKey) ([]byte, error) {
		pubHex := fmt.Sprintf("%x", schnorr.SerializePubKey(key.PubKey()))
		sig, ok := sigs[pubHex]
		if !ok {
			return nil, fmt.Errorf("signature not found for key %s", pubHex[:16])
		}
		return sig, nil
	}

	switch leaf {
	case EscrowLeafRelease:
		if preimage == nil {
			return fmt.Errorf("preimage is required for release leaf")
		}
		sig, err := getSig(keys[0])
		if err != nil {
			return err
		}
		p.Inputs[0].FinalScriptWitness = serializeWitness([][]byte{sig, preimage, leafScript, controlBlock})

	case EscrowLeafTimeout:
		sig, err := getSig(keys[0])
		if err != nil {
			return err
		}
		p.Inputs[0].FinalScriptWitness = serializeWitness([][]byte{sig, leafScript, controlBlock})

	case EscrowLeafDisputeSeller, EscrowLeafDisputeBuyer:
		if len(keys) < 2 {
			return fmt.Errorf("two signing keys required for dispute leaf")
		}
		// keys[0] = oracle, keys[1] = winner; witness order is reverse of script order
		oracleSig, err := getSig(keys[0])
		if err != nil {
			return err
		}
		winnerSig, err := getSig(keys[1])
		if err != nil {
			return err
		}
		p.Inputs[0].FinalScriptWitness = serializeWitness([][]byte{winnerSig, oracleSig, leafScript, controlBlock})

	default:
		return fmt.Errorf("unknown escrow leaf %d", leaf)
	}

	return nil
}
