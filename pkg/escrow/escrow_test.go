package escrow

import (
	"crypto/sha256"
	"testing"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/stretchr/testify/require"
)

func TestGenerateSecret(t *testing.T) {
	secret, hash, err := GenerateSecret()
	require.NoError(t, err)
	require.NotEqual(t, [SecretSize]byte{}, secret)
	require.Equal(t, sha256.Sum256(secret[:]), hash)
}

func TestNewEscrowScript(t *testing.T) {
	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	_, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	params := EscrowParams{
		SellerPubKey: sellerKey.PubKey(),
		BuyerPubKey:  buyerKey.PubKey(),
		OraclePubKey: oracleKey.PubKey(),
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: DefaultTimeoutBlocks,
		},
	}

	es, err := NewEscrowScript(params)
	require.NoError(t, err)
	require.Len(t, es.Closures, 4)

	// Verify each leaf type
	_, ok := es.Closures[0].(*script.ConditionMultisigClosure)
	require.True(t, ok, "leaf 0 should be ConditionMultisigClosure")

	_, ok = es.Closures[1].(*script.CSVMultisigClosure)
	require.True(t, ok, "leaf 1 should be CSVMultisigClosure")

	_, ok = es.Closures[2].(*script.MultisigClosure)
	require.True(t, ok, "leaf 2 should be MultisigClosure")

	_, ok = es.Closures[3].(*script.MultisigClosure)
	require.True(t, ok, "leaf 3 should be MultisigClosure")
}

func TestEscrowTapTree(t *testing.T) {
	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	_, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	params := EscrowParams{
		SellerPubKey: sellerKey.PubKey(),
		BuyerPubKey:  buyerKey.PubKey(),
		OraclePubKey: oracleKey.PubKey(),
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: DefaultTimeoutBlocks,
		},
	}

	es, err := NewEscrowScript(params)
	require.NoError(t, err)

	key, err := es.TapTree()
	require.NoError(t, err)
	require.NotNil(t, key)

	// Verify deterministic — same params produce same key
	es2, err := NewEscrowScript(params)
	require.NoError(t, err)
	key2, err := es2.TapTree()
	require.NoError(t, err)
	require.Equal(t, schnorr.SerializePubKey(key), schnorr.SerializePubKey(key2))
}

func TestEscrowAddress(t *testing.T) {
	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	_, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	params := EscrowParams{
		SellerPubKey: sellerKey.PubKey(),
		BuyerPubKey:  buyerKey.PubKey(),
		OraclePubKey: oracleKey.PubKey(),
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: DefaultTimeoutBlocks,
		},
	}

	es, err := NewEscrowScript(params)
	require.NoError(t, err)

	addr, err := es.Address("ert")
	require.NoError(t, err)
	require.NotEmpty(t, addr)
	// Liquid regtest taproot addresses start with "ert1p"
	require.Contains(t, addr, "ert1p")
}

func TestReleaseCondition(t *testing.T) {
	// Verify the release leaf's condition script works with SHA256 preimage
	secret, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	condition, err := txscript.NewScriptBuilder().
		AddOp(txscript.OP_SHA256).
		AddData(secretHash[:]).
		AddOp(txscript.OP_EQUAL).
		Script()
	require.NoError(t, err)

	// Valid preimage
	result, err := script.EvaluateScriptToBool(condition, wire.TxWitness{secret[:]})
	require.NoError(t, err)
	require.True(t, result)

	// Invalid preimage
	badSecret := [SecretSize]byte{0x01}
	result, err = script.EvaluateScriptToBool(condition, wire.TxWitness{badSecret[:]})
	require.NoError(t, err)
	require.False(t, result)
}

func TestEscrowScriptEncodeDecode(t *testing.T) {
	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	_, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	params := EscrowParams{
		SellerPubKey: sellerKey.PubKey(),
		BuyerPubKey:  buyerKey.PubKey(),
		OraclePubKey: oracleKey.PubKey(),
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: DefaultTimeoutBlocks,
		},
	}

	es, err := NewEscrowScript(params)
	require.NoError(t, err)

	// Encode each closure to script bytes, then decode back via DecodeClosure
	for i, c := range es.Closures {
		scriptBytes, err := c.Script()
		require.NoError(t, err, "leaf %d", i)

		decoded, err := script.DecodeClosure(scriptBytes)
		require.NoError(t, err, "leaf %d decode", i)

		// Verify decoded closure produces the same script
		reEncoded, err := decoded.Script()
		require.NoError(t, err, "leaf %d re-encode", i)
		require.Equal(t, scriptBytes, reEncoded, "leaf %d round-trip", i)
	}
}

func TestMissingKeys(t *testing.T) {
	_, err := NewEscrowScript(EscrowParams{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "all public keys must be provided")
}
