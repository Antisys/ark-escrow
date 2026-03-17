package escrow

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
)

const (
	SecretSize           = 32
	DefaultTimeoutBlocks = 144
)

type EscrowParams struct {
	SellerPubKey *btcec.PublicKey
	BuyerPubKey  *btcec.PublicKey
	OraclePubKey *btcec.PublicKey
	SecretHash   [32]byte
	Timeout      script.RelativeLocktime
}

type EscrowScript struct {
	Params   EscrowParams
	Closures []script.Closure
}

func GenerateSecret() (secret [SecretSize]byte, hash [32]byte, err error) {
	_, err = rand.Read(secret[:])
	if err != nil {
		return secret, hash, fmt.Errorf("failed to generate random secret: %w", err)
	}
	hash = sha256.Sum256(secret[:])
	return
}

func NewEscrowScript(params EscrowParams) (*EscrowScript, error) {
	if params.SellerPubKey == nil || params.BuyerPubKey == nil || params.OraclePubKey == nil {
		return nil, fmt.Errorf("all public keys must be provided")
	}

	condition, err := txscript.NewScriptBuilder().
		AddOp(txscript.OP_SHA256).
		AddData(params.SecretHash[:]).
		AddOp(txscript.OP_EQUAL).
		Script()
	if err != nil {
		return nil, fmt.Errorf("failed to build release condition: %w", err)
	}

	closures := []script.Closure{
		&script.ConditionMultisigClosure{
			MultisigClosure: script.MultisigClosure{
				PubKeys: []*btcec.PublicKey{params.SellerPubKey},
				Type:    script.MultisigTypeChecksig,
			},
			Condition: condition,
		},
		&script.CSVMultisigClosure{
			MultisigClosure: script.MultisigClosure{
				PubKeys: []*btcec.PublicKey{params.BuyerPubKey},
				Type:    script.MultisigTypeChecksig,
			},
			Locktime: params.Timeout,
		},
		&script.MultisigClosure{
			PubKeys: []*btcec.PublicKey{params.OraclePubKey, params.SellerPubKey},
			Type:    script.MultisigTypeChecksig,
		},
		&script.MultisigClosure{
			PubKeys: []*btcec.PublicKey{params.OraclePubKey, params.BuyerPubKey},
			Type:    script.MultisigTypeChecksig,
		},
	}

	return &EscrowScript{Params: params, Closures: closures}, nil
}

func (e *EscrowScript) TapTree() (*btcec.PublicKey, error) {
	key, _, err := ElementsTapTree(e.Closures)
	return key, err
}

func (e *EscrowScript) Address(hrp string) (string, error) {
	return ElementsAddress(e.Closures, hrp)
}

func (e *EscrowScript) ReleaseClosure() *script.ConditionMultisigClosure {
	return e.Closures[0].(*script.ConditionMultisigClosure)
}

func (e *EscrowScript) TimeoutClosure() *script.CSVMultisigClosure {
	return e.Closures[1].(*script.CSVMultisigClosure)
}

func (e *EscrowScript) DisputeSellerClosure() *script.MultisigClosure {
	return e.Closures[2].(*script.MultisigClosure)
}

func (e *EscrowScript) DisputeBuyerClosure() *script.MultisigClosure {
	return e.Closures[3].(*script.MultisigClosure)
}
