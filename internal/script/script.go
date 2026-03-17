package script

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// MultisigType indicates the signature aggregation scheme.
type MultisigType int

const (
	MultisigTypeChecksig MultisigType = iota
	MultisigTypeChecksigAdd
)

// Closure is a tapscript leaf that can produce a script and decode from one.
type Closure interface {
	Script() ([]byte, error)
	Decode(script []byte) (bool, error)
	Witness(controlBlock []byte, opts map[string][]byte) (wire.TxWitness, error)
}

// 0250929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0
var unspendablePoint = []byte{
	0x02, 0x50, 0x92, 0x9b, 0x74, 0xc1, 0xa0, 0x49, 0x54, 0xb7, 0x8b, 0x4b, 0x60, 0x35, 0xe9, 0x7a,
	0x5e, 0x07, 0x8a, 0x5a, 0x0f, 0x28, 0xec, 0x96, 0xd5, 0x47, 0xbf, 0xee, 0x9a, 0xce, 0x80, 0x3a, 0xc0,
}

// UnspendableKey returns the provably unspendable internal key for taproot script-only outputs.
func UnspendableKey() *btcec.PublicKey {
	key, _ := btcec.ParsePubKey(unspendablePoint)
	return key
}

// P2TRScript returns the witness program for a taproot output key.
func P2TRScript(taprootKey *btcec.PublicKey) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddOp(txscript.OP_1).
		AddData(schnorr.SerializePubKey(taprootKey)).
		Script()
}

// forbiddenOpcodes are opcodes not allowed in a condition script.
var forbiddenOpcodes = []byte{
	txscript.OP_CHECKMULTISIG,
	txscript.OP_CHECKSIG,
	txscript.OP_CHECKSIGVERIFY,
	txscript.OP_CHECKSIGADD,
	txscript.OP_CHECKMULTISIGVERIFY,
	txscript.OP_CHECKLOCKTIMEVERIFY,
	txscript.OP_CHECKSEQUENCEVERIFY,
}

// EvaluateScriptToBool executes a condition script with the provided witness
// and returns a boolean result. Used for testing preimage conditions.
func EvaluateScriptToBool(script []byte, witness wire.TxWitness) (bool, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	for tokenizer.Next() {
		for _, opcode := range forbiddenOpcodes {
			if tokenizer.OpcodePosition() != -1 && tokenizer.Opcode() == opcode {
				return false, fmt.Errorf("forbidden opcode %x", opcode)
			}
		}
	}

	fakeTx := &wire.MsgTx{
		Version: 2,
		TxIn:    []*wire.TxIn{{Sequence: 0xffffffff}},
		TxOut:   []*wire.TxOut{{Value: 0}},
	}

	vm, err := txscript.NewEngine(
		script, fakeTx, 0,
		txscript.ScriptVerifyTaproot,
		nil, nil, 0, nil,
	)
	if err != nil {
		return false, fmt.Errorf("failed to create script engine: %w", err)
	}

	vm.SetStack(witness)

	if err := vm.Execute(); err != nil {
		if scriptError, ok := err.(txscript.Error); ok {
			if scriptError.ErrorCode == txscript.ErrEvalFalse {
				return false, nil
			}
		}
		return false, err
	}

	finalStack := vm.GetStack()
	if len(finalStack) != 0 {
		return false, fmt.Errorf("script must return zero value on the stack, got %d", len(finalStack))
	}

	return true, nil
}
