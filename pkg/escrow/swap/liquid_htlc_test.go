package swap

import (
	"crypto/sha256"
	"testing"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/stretchr/testify/require"
)

func TestNewHTLCScript(t *testing.T) {
	receiverKey, _ := btcec.NewPrivateKey()
	senderKey, _ := btcec.NewPrivateKey()
	hash := sha256.Sum256([]byte{1, 2, 3, 4})

	htlc, err := NewHTLCScript(receiverKey.PubKey(), senderKey.PubKey(), hash, HTLCDefaultTimeout)
	require.NoError(t, err)
	require.Len(t, htlc.Closures, 2)

	_, ok := htlc.Closures[0].(*script.ConditionMultisigClosure)
	require.True(t, ok)
	_, ok = htlc.Closures[1].(*script.CSVMultisigClosure)
	require.True(t, ok)
}

func TestHTLCTapTree(t *testing.T) {
	receiverKey, _ := btcec.NewPrivateKey()
	senderKey, _ := btcec.NewPrivateKey()
	hash := sha256.Sum256([]byte("test preimage"))

	htlc, err := NewHTLCScript(receiverKey.PubKey(), senderKey.PubKey(), hash, HTLCDefaultTimeout)
	require.NoError(t, err)

	key, _, err := escrow.ElementsTapTree(htlc.Closures)
	require.NoError(t, err)
	require.NotNil(t, key)

	key2, _, err := escrow.ElementsTapTree(htlc.Closures)
	require.NoError(t, err)
	require.Equal(t, schnorr.SerializePubKey(key), schnorr.SerializePubKey(key2))
}

func TestHTLCAddress(t *testing.T) {
	receiverKey, _ := btcec.NewPrivateKey()
	senderKey, _ := btcec.NewPrivateKey()
	hash := sha256.Sum256([]byte("test"))

	htlc, err := NewHTLCScript(receiverKey.PubKey(), senderKey.PubKey(), hash, HTLCDefaultTimeout)
	require.NoError(t, err)

	addr, err := htlc.Address("ert")
	require.NoError(t, err)
	require.Contains(t, addr, "ert1p")
}

func TestHTLCLeafScript(t *testing.T) {
	receiverKey, _ := btcec.NewPrivateKey()
	senderKey, _ := btcec.NewPrivateKey()
	hash := sha256.Sum256([]byte("test"))

	htlc, err := NewHTLCScript(receiverKey.PubKey(), senderKey.PubKey(), hash, HTLCDefaultTimeout)
	require.NoError(t, err)

	s0, cb0, err := htlc.LeafScript(0)
	require.NoError(t, err)
	require.NotEmpty(t, s0)
	require.NotEmpty(t, cb0)

	s1, cb1, err := htlc.LeafScript(1)
	require.NoError(t, err)
	require.NotEqual(t, s0, s1)
	_ = cb1

	_, _, err = htlc.LeafScript(2)
	require.Error(t, err)
}

func TestHTLCClaimCondition(t *testing.T) {
	preimage := [32]byte{0xaa, 0xbb, 0xcc}
	hash := sha256.Sum256(preimage[:])

	condition, err := txscript.NewScriptBuilder().
		AddOp(txscript.OP_SHA256).AddData(hash[:]).AddOp(txscript.OP_EQUAL).Script()
	require.NoError(t, err)

	result, err := script.EvaluateScriptToBool(condition, wire.TxWitness{preimage[:]})
	require.NoError(t, err)
	require.True(t, result)

	result, err = script.EvaluateScriptToBool(condition, wire.TxWitness{make([]byte, 32)})
	require.NoError(t, err)
	require.False(t, result)
}

func TestHTLCRefundClosureTimeout(t *testing.T) {
	receiverKey, _ := btcec.NewPrivateKey()
	senderKey, _ := btcec.NewPrivateKey()
	hash := sha256.Sum256([]byte("test"))

	htlc, err := NewHTLCScript(receiverKey.PubKey(), senderKey.PubKey(), hash, 50)
	require.NoError(t, err)

	refund := htlc.RefundClosure()
	require.Equal(t, script.LocktimeTypeBlock, refund.Locktime.Type)
	require.Equal(t, uint32(50), refund.Locktime.Value)
	require.Equal(t, schnorr.SerializePubKey(senderKey.PubKey()), schnorr.SerializePubKey(refund.PubKeys[0]))
}

func TestHTLCClaimClosureKeys(t *testing.T) {
	receiverKey, _ := btcec.NewPrivateKey()
	senderKey, _ := btcec.NewPrivateKey()
	hash := sha256.Sum256([]byte("test"))

	htlc, err := NewHTLCScript(receiverKey.PubKey(), senderKey.PubKey(), hash, HTLCDefaultTimeout)
	require.NoError(t, err)

	claim := htlc.ClaimClosure()
	require.Equal(t, schnorr.SerializePubKey(receiverKey.PubKey()), schnorr.SerializePubKey(claim.PubKeys[0]))
}

func TestHTLCMissingKeys(t *testing.T) {
	_, err := NewHTLCScript(nil, nil, [32]byte{}, 10)
	require.Error(t, err)
}

func TestBech32Roundtrip(t *testing.T) {
	receiverKey, _ := btcec.NewPrivateKey()
	senderKey, _ := btcec.NewPrivateKey()
	hash := sha256.Sum256([]byte("roundtrip"))

	htlc, err := NewHTLCScript(receiverKey.PubKey(), senderKey.PubKey(), hash, 10)
	require.NoError(t, err)

	addr, err := htlc.Address("ert")
	require.NoError(t, err)

	decoded, err := escrow.AddressToOutputScript(addr)
	require.NoError(t, err)

	p2tr, err := htlc.P2TRScript()
	require.NoError(t, err)
	require.Equal(t, p2tr, decoded)
}
