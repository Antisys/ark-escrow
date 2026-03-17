package swap

import (
	"context"
	"encoding/hex"
	"math/rand"
	"net/http"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/stretchr/testify/require"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/Antisys/ark-escrow/pkg/escrow"
)

func randomTestEscrow(t *testing.T, rng *rand.Rand) (
	*escrow.EscrowScript,
	*btcec.PrivateKey, *btcec.PrivateKey, *btcec.PrivateKey,
	[escrow.SecretSize]byte, uint32,
) {
	t.Helper()

	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	secret, secretHash, err := escrow.GenerateSecret()
	require.NoError(t, err)

	timeout := uint32(rng.Intn(2016-144+1)) + 144

	es, err := escrow.NewEscrowScript(escrow.EscrowParams{
		SellerPubKey: sellerKey.PubKey(),
		BuyerPubKey:  buyerKey.PubKey(),
		OraclePubKey: oracleKey.PubKey(),
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: timeout,
		},
	})
	require.NoError(t, err)

	return es, sellerKey, buyerKey, oracleKey, secret, timeout
}

func randomTxID(rng *rand.Rand) string {
	b := make([]byte, 32)
	rng.Read(b)
	return hex.EncodeToString(b)
}

// validationError returns the validation error from ClaimEscrow, if any.
// If the config passes validation and reaches the signing/broadcast stage, returns "".
func validationError(cfg ClaimEscrowConfig) string {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Create a dummy client with a real http.Client that will fail on the canceled context.
	dummyRPC := &elementsdRPC{
		httpClient: &http.Client{},
	}
	dummyClient := &ElementsdClient{rpc: dummyRPC}
	_, err := ClaimEscrow(ctx, dummyClient, cfg)
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestRandomizedClaimValidationRelease tests release leaf validation with random params.
func TestRandomizedClaimValidationRelease(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))
	iterations := 50

	for i := 0; i < iterations; i++ {
		es, sellerKey, _, _, secret, _ := randomTestEscrow(t, rng)
		amount := uint64(rng.Intn(1_000_000_000)) + 2000
		fee := uint64(1000)

		cfg := ClaimEscrowConfig{
			EscrowScript: es,
			FundTxID:     randomTxID(rng),
			FundVout:     uint32(rng.Intn(10)),
			Amount:       amount,
			Fee:          fee,
			Leaf:         EscrowLeafRelease,
			SigningKeys:  []*btcec.PrivateKey{sellerKey},
			Preimage:     secret[:],
			DestAddress:  mustRandomAddress(t),
		}

		errMsg := validationError(cfg)
		// Should get past validation (error will be about broadcast/context, not validation)
		require.NotContains(t, errMsg, "escrow script is required", "iteration %d", i)
		require.NotContains(t, errMsg, "signing key", "iteration %d", i)
		require.NotContains(t, errMsg, "destination address", "iteration %d", i)
		require.NotContains(t, errMsg, "below dust", "iteration %d", i)
		require.NotContains(t, errMsg, "invalid leaf index", "iteration %d", i)
	}
}

// TestRandomizedClaimValidationTimeout tests timeout leaf validation with random params.
func TestRandomizedClaimValidationTimeout(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))
	iterations := 50

	for i := 0; i < iterations; i++ {
		es, _, buyerKey, _, _, _ := randomTestEscrow(t, rng)
		amount := uint64(rng.Intn(1_000_000_000)) + 2000

		cfg := ClaimEscrowConfig{
			EscrowScript: es,
			FundTxID:     randomTxID(rng),
			FundVout:     uint32(rng.Intn(10)),
			Amount:       amount,
			Fee:          1000,
			Leaf:         EscrowLeafTimeout,
			SigningKeys:  []*btcec.PrivateKey{buyerKey},
			DestAddress:  mustRandomAddress(t),
		}

		errMsg := validationError(cfg)
		require.NotContains(t, errMsg, "below dust", "iteration %d", i)
		require.NotContains(t, errMsg, "invalid leaf index", "iteration %d", i)
	}
}

// TestRandomizedClaimValidationDispute tests dispute leaf validation with random params.
func TestRandomizedClaimValidationDispute(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))
	iterations := 50

	for i := 0; i < iterations; i++ {
		es, sellerKey, buyerKey, oracleKey, _, _ := randomTestEscrow(t, rng)
		amount := uint64(rng.Intn(1_000_000_000)) + 2000

		// Dispute-seller
		cfg := ClaimEscrowConfig{
			EscrowScript: es,
			FundTxID:     randomTxID(rng),
			FundVout:     uint32(rng.Intn(10)),
			Amount:       amount,
			Fee:          1000,
			Leaf:         EscrowLeafDisputeSeller,
			SigningKeys:  []*btcec.PrivateKey{oracleKey, sellerKey},
			DestAddress:  mustRandomAddress(t),
		}

		errMsg := validationError(cfg)
		require.NotContains(t, errMsg, "two signing keys", "iteration %d: dispute-seller", i)

		// Dispute-buyer
		cfg.Leaf = EscrowLeafDisputeBuyer
		cfg.SigningKeys = []*btcec.PrivateKey{oracleKey, buyerKey}
		errMsg = validationError(cfg)
		require.NotContains(t, errMsg, "two signing keys", "iteration %d: dispute-buyer", i)

		// Single key for dispute must fail at finalize (needs 2 keys)
		cfg.SigningKeys = []*btcec.PrivateKey{oracleKey}
		errMsg = validationError(cfg)
		require.Contains(t, errMsg, "two signing keys", "iteration %d: single key dispute must fail", i)
	}
}

// TestRandomizedDustThreshold tests that random amounts near the dust limit are rejected correctly.
func TestRandomizedDustThreshold(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))
	iterations := 100

	for i := 0; i < iterations; i++ {
		es, sellerKey, _, _, secret, _ := randomTestEscrow(t, rng)
		fee := uint64(rng.Intn(5000)) + 500

		// Amount that results in output <= 546 after fee
		dustAmount := uint64(rng.Intn(546)) + fee + 1 // output = dustAmount - fee, in [1..546]
		cfg := ClaimEscrowConfig{
			EscrowScript: es,
			FundTxID:     randomTxID(rng),
			Amount:       dustAmount,
			Fee:          fee,
			Leaf:         EscrowLeafRelease,
			SigningKeys:  []*btcec.PrivateKey{sellerKey},
			Preimage:     secret[:],
			DestAddress:  mustRandomAddress(t),
		}
		errMsg := validationError(cfg)
		require.Contains(t, errMsg, "dust", "iteration %d: amount %d fee %d output %d should be dust",
			i, dustAmount, fee, dustAmount-fee)

		// Amount well above dust
		safeAmount := fee + 547 + uint64(rng.Intn(1_000_000))
		cfg.Amount = safeAmount
		errMsg = validationError(cfg)
		require.NotContains(t, errMsg, "dust", "iteration %d: amount %d fee %d should NOT be dust",
			i, safeAmount, fee)
	}
}

// TestRandomizedLeafScriptConsistency verifies that leaf scripts from different access paths match.
func TestRandomizedLeafScriptConsistency(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))
	iterations := 50

	for i := 0; i < iterations; i++ {
		es, _, _, _, _, _ := randomTestEscrow(t, rng)

		for leafIdx := 0; leafIdx < 4; leafIdx++ {
			leafScript, _, err := escrow.ElementsLeafScript(es.Closures, leafIdx)
			require.NoError(t, err)

			directScript, err := es.Closures[leafIdx].Script()
			require.NoError(t, err)

			require.Equal(t, directScript, leafScript,
				"iteration %d leaf %d: ElementsLeafScript must match Closure.Script()", i, leafIdx)
		}

		// P2TR from ElementsP2TRScript must match tap tree output
		p2tr, err := escrow.ElementsP2TRScript(es.Closures)
		require.NoError(t, err)

		key, err := es.TapTree()
		require.NoError(t, err)
		keyBytes := schnorr.SerializePubKey(key)

		require.Equal(t, keyBytes, p2tr[2:], "iteration %d: P2TR key must match TapTree output key", i)
	}
}

// TestRandomizedDisputeLeafSymmetry verifies dispute leaves are properly mirrored.
func TestRandomizedDisputeLeafSymmetry(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))
	iterations := 50

	for i := 0; i < iterations; i++ {
		es, _, _, _, _, _ := randomTestEscrow(t, rng)

		ds := es.DisputeSellerClosure()
		db := es.DisputeBuyerClosure()
		require.Len(t, ds.PubKeys, 2, "iteration %d", i)
		require.Len(t, db.PubKeys, 2, "iteration %d", i)

		// Oracle key is first in both
		require.Equal(t,
			schnorr.SerializePubKey(ds.PubKeys[0]),
			schnorr.SerializePubKey(db.PubKeys[0]),
			"iteration %d: oracle key must be same in both dispute leaves", i)

		// Winner keys must differ
		require.NotEqual(t,
			schnorr.SerializePubKey(ds.PubKeys[1]),
			schnorr.SerializePubKey(db.PubKeys[1]),
			"iteration %d: dispute winner keys must differ", i)

		// Scripts must differ
		dsScript, _, err := escrow.ElementsLeafScript(es.Closures, 2)
		require.NoError(t, err)
		dbScript, _, err := escrow.ElementsLeafScript(es.Closures, 3)
		require.NoError(t, err)
		require.NotEqual(t, dsScript, dbScript, "iteration %d: dispute scripts must differ", i)
	}
}

// TestRandomizedInvalidLeafIndex tests that out-of-range leaf indices are rejected.
func TestRandomizedInvalidLeafIndex(t *testing.T) {
	seed := rand.Int63()
	t.Logf("seed: %d", seed)
	rng := rand.New(rand.NewSource(seed))
	iterations := 50

	for i := 0; i < iterations; i++ {
		es, sellerKey, _, _, secret, _ := randomTestEscrow(t, rng)

		invalidLeaf := EscrowLeaf(rng.Intn(100) + 4) // 4..103
		cfg := ClaimEscrowConfig{
			EscrowScript: es,
			FundTxID:     randomTxID(rng),
			Amount:       100000,
			Fee:          1000,
			Leaf:         invalidLeaf,
			SigningKeys:  []*btcec.PrivateKey{sellerKey},
			Preimage:     secret[:],
			DestAddress:  mustRandomAddress(t),
		}
		errMsg := validationError(cfg)
		require.Contains(t, errMsg, "invalid leaf index", "iteration %d: leaf %d", i, invalidLeaf)
	}
}

func mustRandomAddress(t *testing.T) string {
	t.Helper()
	k1, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	k2, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	htlc, err := NewHTLCScript(k1.PubKey(), k2.PubKey(), [32]byte{0x01}, 10)
	require.NoError(t, err)
	addr, err := htlc.Address("ert")
	require.NoError(t, err)
	return addr
}
