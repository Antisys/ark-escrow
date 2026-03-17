package swap

import (
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/stretchr/testify/require"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/Antisys/ark-escrow/pkg/escrow"
)

func newTestEscrowScript(t *testing.T) (*escrow.EscrowScript, *btcec.PrivateKey, *btcec.PrivateKey, *btcec.PrivateKey, [escrow.SecretSize]byte) {
	t.Helper()

	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	secret, secretHash, err := escrow.GenerateSecret()
	require.NoError(t, err)

	es, err := escrow.NewEscrowScript(escrow.EscrowParams{
		SellerPubKey: sellerKey.PubKey(),
		BuyerPubKey:  buyerKey.PubKey(),
		OraclePubKey: oracleKey.PubKey(),
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: escrow.DefaultTimeoutBlocks,
		},
	})
	require.NoError(t, err)

	return es, sellerKey, buyerKey, oracleKey, secret
}

func TestEscrowLeafScript(t *testing.T) {
	es, _, _, _, _ := newTestEscrowScript(t)

	for i := 0; i < 4; i++ {
		t.Run(leafName(EscrowLeaf(i)), func(t *testing.T) {
			s, cb, err := escrow.ElementsLeafScript(es.Closures, i)
			require.NoError(t, err)
			require.NotEmpty(t, s)
			require.NotEmpty(t, cb)
		})
	}

	_, _, err := escrow.ElementsLeafScript(es.Closures, 4)
	require.Error(t, err)
}

func TestEscrowP2TRScript(t *testing.T) {
	es, _, _, _, _ := newTestEscrowScript(t)

	p2tr, err := escrow.ElementsP2TRScript(es.Closures)
	require.NoError(t, err)
	require.Len(t, p2tr, 34)
	require.Equal(t, byte(0x51), p2tr[0])
	require.Equal(t, byte(0x20), p2tr[1])
}

func TestEscrowLeafScriptDeterministic(t *testing.T) {
	es, _, _, _, _ := newTestEscrowScript(t)

	s1, cb1, err := escrow.ElementsLeafScript(es.Closures, 0)
	require.NoError(t, err)
	s2, cb2, err := escrow.ElementsLeafScript(es.Closures, 0)
	require.NoError(t, err)
	require.Equal(t, s1, s2)
	require.Equal(t, cb1, cb2)
}

func TestEscrowLeafScriptsUnique(t *testing.T) {
	es, _, _, _, _ := newTestEscrowScript(t)

	scripts := make(map[string]bool)
	for i := 0; i < 4; i++ {
		s, _, err := escrow.ElementsLeafScript(es.Closures, i)
		require.NoError(t, err)
		h := hex.EncodeToString(s)
		require.False(t, scripts[h], "leaf %d script should be unique", i)
		scripts[h] = true
	}
}

func TestClaimEscrowConfigValidation(t *testing.T) {
	es, sellerKey, _, _, secret := newTestEscrowScript(t)

	tests := []struct {
		name string
		cfg  ClaimEscrowConfig
		err  string
	}{
		{"missing escrow script", ClaimEscrowConfig{}, "escrow script is required"},
		{"missing signing keys", ClaimEscrowConfig{EscrowScript: es}, "at least one signing key is required"},
		{"missing dest address", ClaimEscrowConfig{EscrowScript: es, SigningKeys: []*btcec.PrivateKey{sellerKey}}, "destination address is required"},
		{"invalid leaf index", ClaimEscrowConfig{EscrowScript: es, SigningKeys: []*btcec.PrivateKey{sellerKey}, DestAddress: "x", Leaf: EscrowLeaf(99)}, "invalid leaf index"},
		{"amount below dust", ClaimEscrowConfig{
			EscrowScript: es, SigningKeys: []*btcec.PrivateKey{sellerKey},
			DestAddress: mustHTLCAddress(t, sellerKey, sellerKey), Leaf: EscrowLeafRelease,
			Amount: 100, Preimage: secret[:],
			FundTxID: "0000000000000000000000000000000000000000000000000000000000000000",
		}, "below dust"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ClaimEscrow(nil, nil, tc.cfg)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestLeafClosureKeys(t *testing.T) {
	es, sellerKey, buyerKey, oracleKey, _ := newTestEscrowScript(t)

	// Release leaf has seller key
	release := es.ReleaseClosure()
	require.Len(t, release.PubKeys, 1)
	require.Equal(t, schnorr.SerializePubKey(sellerKey.PubKey()), schnorr.SerializePubKey(release.PubKeys[0]))

	// Timeout leaf has buyer key
	timeout := es.TimeoutClosure()
	require.Len(t, timeout.PubKeys, 1)
	require.Equal(t, schnorr.SerializePubKey(buyerKey.PubKey()), schnorr.SerializePubKey(timeout.PubKeys[0]))

	// Dispute→seller has oracle + seller
	ds := es.DisputeSellerClosure()
	require.Len(t, ds.PubKeys, 2)
	require.Equal(t, schnorr.SerializePubKey(oracleKey.PubKey()), schnorr.SerializePubKey(ds.PubKeys[0]))
	require.Equal(t, schnorr.SerializePubKey(sellerKey.PubKey()), schnorr.SerializePubKey(ds.PubKeys[1]))

	// Dispute→buyer has oracle + buyer
	db := es.DisputeBuyerClosure()
	require.Len(t, db.PubKeys, 2)
	require.Equal(t, schnorr.SerializePubKey(oracleKey.PubKey()), schnorr.SerializePubKey(db.PubKeys[0]))
	require.Equal(t, schnorr.SerializePubKey(buyerKey.PubKey()), schnorr.SerializePubKey(db.PubKeys[1]))
}

func mustHTLCAddress(t *testing.T, key1, key2 *btcec.PrivateKey) string {
	t.Helper()
	htlc, err := NewHTLCScript(key1.PubKey(), key2.PubKey(), [32]byte{0x01}, 10)
	require.NoError(t, err)
	addr, err := htlc.Address("ert")
	require.NoError(t, err)
	return addr
}

func leafName(l EscrowLeaf) string {
	switch l {
	case EscrowLeafRelease:
		return "release"
	case EscrowLeafTimeout:
		return "timeout"
	case EscrowLeafDisputeSeller:
		return "dispute-seller"
	case EscrowLeafDisputeBuyer:
		return "dispute-buyer"
	default:
		return "unknown"
	}
}
