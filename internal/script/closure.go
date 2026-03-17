package script

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// MultisigClosure is a closure that contains public keys with CHECKSIG/CHECKSIGVERIFY.
type MultisigClosure struct {
	PubKeys []*btcec.PublicKey
	Type    MultisigType
}

func (f *MultisigClosure) Script() ([]byte, error) {
	scriptBuilder := txscript.NewScriptBuilder()

	switch f.Type {
	case MultisigTypeChecksig:
		for i, pubkey := range f.PubKeys {
			scriptBuilder.AddData(schnorr.SerializePubKey(pubkey))
			if i == len(f.PubKeys)-1 {
				scriptBuilder.AddOp(txscript.OP_CHECKSIG)
				continue
			}
			scriptBuilder.AddOp(txscript.OP_CHECKSIGVERIFY)
		}
	case MultisigTypeChecksigAdd:
		for i, pubkey := range f.PubKeys {
			scriptBuilder.AddData(schnorr.SerializePubKey(pubkey))
			if i == 0 {
				scriptBuilder.AddOp(txscript.OP_CHECKSIG)
				continue
			}
			scriptBuilder.AddOp(txscript.OP_CHECKSIGADD)
		}
		scriptBuilder.AddInt64(int64(len(f.PubKeys)))
		scriptBuilder.AddOp(txscript.OP_NUMEQUAL)
	}

	return scriptBuilder.Script()
}

func (f *MultisigClosure) Decode(script []byte) (bool, error) {
	if len(script) == 0 {
		return false, fmt.Errorf("failed to decode: script is empty")
	}

	valid, err := f.decodeChecksig(script)
	if err != nil {
		return false, fmt.Errorf("failed to decode checksig: %w", err)
	}
	if valid {
		return true, nil
	}

	valid, err = f.decodeChecksigAdd(script)
	if err != nil {
		return false, fmt.Errorf("failed to decode checksigadd: %w", err)
	}
	return valid, nil
}

func (f *MultisigClosure) decodeChecksig(script []byte) (bool, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	pubkeys := make([]*btcec.PublicKey, 0)

	for tokenizer.Next() {
		if tokenizer.Opcode() != txscript.OP_DATA_32 {
			return false, nil
		}
		pubkey, err := schnorr.ParsePubKey(tokenizer.Data())
		if err != nil {
			return false, err
		}
		pubkeys = append(pubkeys, pubkey)
		if !tokenizer.Next() {
			return false, nil
		}
		if tokenizer.Opcode() == txscript.OP_CHECKSIGVERIFY {
			continue
		} else {
			break
		}
	}

	if tokenizer.Err() != nil || tokenizer.Opcode() != txscript.OP_CHECKSIG {
		return false, nil
	}
	if len(pubkeys) == 0 {
		return false, nil
	}

	f.PubKeys = pubkeys
	f.Type = MultisigTypeChecksig

	rebuilt, err := f.Script()
	if err != nil {
		f.PubKeys = nil
		f.Type = 0
		return false, err
	}
	if !bytes.Equal(rebuilt, script) {
		f.PubKeys = nil
		f.Type = 0
		return false, nil
	}

	return true, nil
}

func (f *MultisigClosure) decodeChecksigAdd(script []byte) (bool, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	pubkeys := make([]*btcec.PublicKey, 0)

	for tokenizer.Next() {
		if txscript.IsSmallInt(tokenizer.Opcode()) {
			break
		}
		if tokenizer.Opcode() != txscript.OP_DATA_32 {
			return false, nil
		}
		pubkey, err := schnorr.ParsePubKey(tokenizer.Data())
		if err != nil {
			return false, err
		}
		pubkeys = append(pubkeys, pubkey)
		if !tokenizer.Next() {
			return false, nil
		}
		if tokenizer.Opcode() == txscript.OP_CHECKSIGADD ||
			tokenizer.Opcode() == txscript.OP_CHECKSIG {
			continue
		} else {
			return false, nil
		}
	}

	if tokenizer.Err() != nil || len(pubkeys) != txscript.AsSmallInt(tokenizer.Opcode()) {
		return false, nil
	}
	if !tokenizer.Next() || tokenizer.Opcode() != txscript.OP_NUMEQUAL {
		return false, nil
	}

	f.PubKeys = pubkeys
	f.Type = MultisigTypeChecksigAdd

	rebuilt, err := f.Script()
	if err != nil {
		f.PubKeys = nil
		f.Type = 0
		return false, err
	}
	if !bytes.Equal(rebuilt, script) {
		f.PubKeys = nil
		f.Type = 0
		return false, nil
	}

	return true, nil
}

func (f *MultisigClosure) Witness(
	controlBlock []byte, signatures map[string][]byte,
) (wire.TxWitness, error) {
	witness := make(wire.TxWitness, 0, len(f.PubKeys)+2)

	for i := len(f.PubKeys) - 1; i >= 0; i-- {
		pubkey := f.PubKeys[i]
		xOnlyPubkey := schnorr.SerializePubKey(pubkey)
		sig, ok := signatures[hex.EncodeToString(xOnlyPubkey)]
		if !ok {
			return nil, fmt.Errorf("missing signature for pubkey %x", xOnlyPubkey)
		}
		witness = append(witness, sig)
	}

	script, err := f.Script()
	if err != nil {
		return nil, fmt.Errorf("failed to generate script: %w", err)
	}

	witness = append(witness, script)
	witness = append(witness, controlBlock)

	return witness, nil
}

// CSVMultisigClosure is a closure with CHECKSEQUENCEVERIFY + multisig.
type CSVMultisigClosure struct {
	MultisigClosure
	Locktime RelativeLocktime
}

func (f *CSVMultisigClosure) Witness(
	controlBlock []byte, signatures map[string][]byte,
) (wire.TxWitness, error) {
	multisigWitness, err := f.MultisigClosure.Witness(controlBlock, signatures)
	if err != nil {
		return nil, err
	}

	script, err := f.Script()
	if err != nil {
		return nil, fmt.Errorf("failed to generate script: %w", err)
	}

	multisigWitness[len(multisigWitness)-2] = script
	return multisigWitness, nil
}

func (d *CSVMultisigClosure) Script() ([]byte, error) {
	sequence, err := BIP68Sequence(d.Locktime)
	if err != nil {
		return nil, err
	}

	csvScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(sequence)).
		AddOps([]byte{
			txscript.OP_CHECKSEQUENCEVERIFY,
			txscript.OP_DROP,
		}).
		Script()
	if err != nil {
		return nil, err
	}

	multisigScript, err := d.MultisigClosure.Script()
	if err != nil {
		return nil, err
	}

	return append(csvScript, multisigScript...), nil
}

func (d *CSVMultisigClosure) Decode(script []byte) (bool, error) {
	if len(script) == 0 {
		return false, fmt.Errorf("empty script")
	}

	tokenizer := txscript.MakeScriptTokenizer(0, script)

	if !tokenizer.Next() {
		return false, nil
	}

	var sequence []byte
	if txscript.IsSmallInt(tokenizer.Opcode()) {
		sequence = []byte{tokenizer.Opcode()}
	} else {
		sequence = tokenizer.Data()
	}

	for _, opCode := range []byte{txscript.OP_CHECKSEQUENCEVERIFY, txscript.OP_DROP} {
		if !tokenizer.Next() || tokenizer.Opcode() != opCode {
			return false, nil
		}
	}

	locktime, err := BIP68DecodeSequenceFromBytes(sequence)
	if err != nil {
		return false, err
	}
	if locktime == nil {
		return false, fmt.Errorf("failed to decode sequence: locktime is nil")
	}

	multisigClosure := &MultisigClosure{}
	subScript := tokenizer.Script()[tokenizer.ByteIndex():]
	valid, err := multisigClosure.Decode(subScript)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, nil
	}

	d.Locktime = *locktime
	d.MultisigClosure = *multisigClosure

	return valid, nil
}

// ConditionMultisigClosure is a closure with a boolean condition + multisig.
type ConditionMultisigClosure struct {
	MultisigClosure
	Condition []byte
}

func (f *ConditionMultisigClosure) Script() ([]byte, error) {
	scriptBuilder := txscript.NewScriptBuilder()

	scriptBuilder.AddOps(f.Condition)
	scriptBuilder.AddOp(txscript.OP_VERIFY)

	multisigScript, err := f.MultisigClosure.Script()
	if err != nil {
		return nil, fmt.Errorf("failed to generate multisig script: %w", err)
	}
	scriptBuilder.AddOps(multisigScript)

	return scriptBuilder.Script()
}

func (f *ConditionMultisigClosure) Decode(script []byte) (bool, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	if len(tokenizer.Script()) == 0 {
		return false, fmt.Errorf("empty script")
	}

	condition := make([]byte, 0)
	for tokenizer.Next() {
		if tokenizer.Opcode() == txscript.OP_VERIFY {
			break
		} else {
			condition = append(condition, tokenizer.Opcode())
			if len(tokenizer.Data()) > 0 {
				condition = append(condition, tokenizer.Data()...)
			}
		}
	}

	f.Condition = condition

	subScript := tokenizer.Script()[tokenizer.ByteIndex():]
	valid, err := f.MultisigClosure.Decode(subScript)
	if err != nil || !valid {
		return false, err
	}

	rebuilt, err := f.Script()
	if err != nil {
		return false, err
	}

	return bytes.Equal(rebuilt, script), nil
}

func (f *ConditionMultisigClosure) Witness(
	controlBlock []byte, args map[string][]byte,
) (wire.TxWitness, error) {
	// The escrow builds witnesses manually in finalizeEscrowWitness,
	// so this method is not used in production. Provided for interface compliance.
	return nil, fmt.Errorf("ConditionMultisigClosure.Witness: use finalizeEscrowWitness instead")
}

