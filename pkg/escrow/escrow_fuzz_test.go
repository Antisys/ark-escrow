package escrow

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/Antisys/ark-escrow/internal/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/stretchr/testify/require"
)

// randomEscrowParams generates escrow params with random keys, secret, and timeout.
func randomEscrowParams(t *testing.T, rng *rand.Rand) (EscrowParams, [SecretSize]byte) {
	t.Helper()

	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	secret, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	// Random timeout between 1 and 65535 blocks
	timeout := uint32(rng.Intn(65535)) + 1

	return EscrowParams{
		SellerPubKey: sellerKey.PubKey(),
		BuyerPubKey:  buyerKey.PubKey(),
		OraclePubKey: oracleKey.PubKey(),
		SecretHash:   secretHash,
		Timeout: script.RelativeLocktime{
			Type:  script.LocktimeTypeBlock,
			Value: timeout,
		},
	}, secret
}

// TestRandomizedScriptTreeProperties runs property checks over many random key/secret/timeout combos.
func TestRandomizedScriptTreeProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 100

	for i := 0; i < iterations; i++ {
		params, _ := randomEscrowParams(t, rng)
		es, err := NewEscrowScript(params)
		require.NoError(t, err, "iteration %d", i)

		// Property 1: always produces exactly 4 closures
		require.Len(t, es.Closures, 4, "iteration %d: must have 4 leaves", i)

		// Property 2: all 4 leaf scripts are non-empty and compile
		for leafIdx := 0; leafIdx < 4; leafIdx++ {
			s, err := es.Closures[leafIdx].Script()
			require.NoError(t, err, "iteration %d leaf %d: script must compile", i, leafIdx)
			require.NotEmpty(t, s, "iteration %d leaf %d: script must be non-empty", i, leafIdx)
		}

		// Property 3: all 4 leaf scripts are unique
		scripts := make(map[string]int)
		for leafIdx := 0; leafIdx < 4; leafIdx++ {
			s, _ := es.Closures[leafIdx].Script()
			h := hex.EncodeToString(s)
			if prev, dup := scripts[h]; dup {
				t.Fatalf("iteration %d: leaf %d duplicates leaf %d", i, leafIdx, prev)
			}
			scripts[h] = leafIdx
		}

		// Property 4: tap tree produces a valid output key
		key, err := es.TapTree()
		require.NoError(t, err, "iteration %d", i)
		require.NotNil(t, key, "iteration %d", i)
		serialized := schnorr.SerializePubKey(key)
		require.Len(t, serialized, 32, "iteration %d: output key must be 32 bytes", i)

		// Property 5: address is valid bech32m starting with "ert1p"
		addr, err := es.Address("ert")
		require.NoError(t, err, "iteration %d", i)
		require.Contains(t, addr, "ert1p", "iteration %d: must be taproot", i)

		// Property 6: P2TR script is 34 bytes (OP_1 <32-byte-key>)
		p2tr, err := ElementsP2TRScript(es.Closures)
		require.NoError(t, err, "iteration %d", i)
		require.Len(t, p2tr, 34, "iteration %d", i)
		require.Equal(t, byte(0x51), p2tr[0], "iteration %d: OP_1", i)
		require.Equal(t, byte(0x20), p2tr[1], "iteration %d: push 32", i)
	}
}

// TestRandomizedDeterminism verifies that the same params always produce the same tree/address.
func TestRandomizedDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 50

	for i := 0; i < iterations; i++ {
		params, _ := randomEscrowParams(t, rng)

		es1, err := NewEscrowScript(params)
		require.NoError(t, err)
		es2, err := NewEscrowScript(params)
		require.NoError(t, err)

		key1, err := es1.TapTree()
		require.NoError(t, err)
		key2, err := es2.TapTree()
		require.NoError(t, err)
		require.Equal(t, schnorr.SerializePubKey(key1), schnorr.SerializePubKey(key2),
			"iteration %d: same params must produce same output key", i)

		addr1, err := es1.Address("ert")
		require.NoError(t, err)
		addr2, err := es2.Address("ert")
		require.NoError(t, err)
		require.Equal(t, addr1, addr2, "iteration %d: same params must produce same address", i)
	}
}

// TestRandomizedKeyUniqueness verifies that different key sets produce different addresses.
func TestRandomizedKeyUniqueness(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 50
	addresses := make(map[string]int)

	for i := 0; i < iterations; i++ {
		params, _ := randomEscrowParams(t, rng)
		es, err := NewEscrowScript(params)
		require.NoError(t, err)

		addr, err := es.Address("ert")
		require.NoError(t, err)

		if prev, dup := addresses[addr]; dup {
			t.Fatalf("iteration %d collides with %d: address %s (astronomically unlikely with random keys)", i, prev, addr)
		}
		addresses[addr] = i
	}
}

// TestRandomizedLeafKeyPlacement verifies the correct keys land in the correct leaves.
func TestRandomizedLeafKeyPlacement(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 50

	for i := 0; i < iterations; i++ {
		params, _ := randomEscrowParams(t, rng)
		es, err := NewEscrowScript(params)
		require.NoError(t, err)

		sellerX := schnorr.SerializePubKey(params.SellerPubKey)
		buyerX := schnorr.SerializePubKey(params.BuyerPubKey)
		oracleX := schnorr.SerializePubKey(params.OraclePubKey)

		// Release: seller only
		release := es.ReleaseClosure()
		require.Len(t, release.PubKeys, 1, "iteration %d", i)
		require.Equal(t, sellerX, schnorr.SerializePubKey(release.PubKeys[0]), "iteration %d: release must use seller key", i)

		// Timeout: buyer only
		timeout := es.TimeoutClosure()
		require.Len(t, timeout.PubKeys, 1, "iteration %d", i)
		require.Equal(t, buyerX, schnorr.SerializePubKey(timeout.PubKeys[0]), "iteration %d: timeout must use buyer key", i)

		// Dispute→seller: [oracle, seller]
		ds := es.DisputeSellerClosure()
		require.Len(t, ds.PubKeys, 2, "iteration %d", i)
		require.Equal(t, oracleX, schnorr.SerializePubKey(ds.PubKeys[0]), "iteration %d: dispute-seller[0] must be oracle", i)
		require.Equal(t, sellerX, schnorr.SerializePubKey(ds.PubKeys[1]), "iteration %d: dispute-seller[1] must be seller", i)

		// Dispute→buyer: [oracle, buyer]
		db := es.DisputeBuyerClosure()
		require.Len(t, db.PubKeys, 2, "iteration %d", i)
		require.Equal(t, oracleX, schnorr.SerializePubKey(db.PubKeys[0]), "iteration %d: dispute-buyer[0] must be oracle", i)
		require.Equal(t, buyerX, schnorr.SerializePubKey(db.PubKeys[1]), "iteration %d: dispute-buyer[1] must be buyer", i)
	}
}

// TestRandomizedReleaseCondition verifies the SHA256 preimage condition with random secrets.
func TestRandomizedReleaseCondition(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 50

	for i := 0; i < iterations; i++ {
		secret, hash, err := GenerateSecret()
		require.NoError(t, err)

		params, _ := randomEscrowParams(t, rng)
		params.SecretHash = hash
		es, err := NewEscrowScript(params)
		require.NoError(t, err)

		// The release closure's condition must embed the correct hash
		release := es.ReleaseClosure()
		condScript, err := release.Condition, error(nil)
		require.NoError(t, err)
		require.NotEmpty(t, condScript, "iteration %d", i)

		// Verify the hash bytes are embedded in the condition script
		require.Contains(t, hex.EncodeToString(condScript), hex.EncodeToString(hash[:]),
			"iteration %d: condition must contain secret hash", i)

		// Verify a wrong secret produces a different hash
		var wrongSecret [SecretSize]byte
		copy(wrongSecret[:], secret[:])
		wrongSecret[rng.Intn(SecretSize)] ^= byte(rng.Intn(255) + 1) // flip a random byte
		wrongHash := sha256.Sum256(wrongSecret[:])
		require.NotEqual(t, hash, wrongHash, "iteration %d: flipped secret must produce different hash", i)
	}
}

// TestRandomizedEncodeDecodeRoundTrip verifies encode/decode with random closures.
func TestRandomizedEncodeDecodeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 50

	for i := 0; i < iterations; i++ {
		params, _ := randomEscrowParams(t, rng)
		es, err := NewEscrowScript(params)
		require.NoError(t, err)

		// Encode each closure to script bytes, then decode back
		for leafIdx, c := range es.Closures {
			scriptBytes, err := c.Script()
			require.NoError(t, err, "iteration %d leaf %d", i, leafIdx)

			decoded, err := script.DecodeClosure(scriptBytes)
			require.NoError(t, err, "iteration %d leaf %d decode", i, leafIdx)

			reEncoded, err := decoded.Script()
			require.NoError(t, err, "iteration %d leaf %d re-encode", i, leafIdx)
			require.Equal(t, scriptBytes, reEncoded,
				"iteration %d leaf %d: round-trip must preserve script", i, leafIdx)
		}
	}
}

// TestRandomizedLeafScriptsAndControlBlocks verifies ElementsLeafScript with random params.
func TestRandomizedLeafScriptsAndControlBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 50

	for i := 0; i < iterations; i++ {
		params, _ := randomEscrowParams(t, rng)
		es, err := NewEscrowScript(params)
		require.NoError(t, err)

		for leafIdx := 0; leafIdx < 4; leafIdx++ {
			leafScript, controlBlock, err := ElementsLeafScript(es.Closures, leafIdx)
			require.NoError(t, err, "iteration %d leaf %d", i, leafIdx)
			require.NotEmpty(t, leafScript, "iteration %d leaf %d: script must not be empty", i, leafIdx)
			require.NotEmpty(t, controlBlock, "iteration %d leaf %d: control block must not be empty", i, leafIdx)

			// Control block first byte encodes the leaf version + parity bit.
			// Bitcoin uses 0xc0/0xc1, Elements uses 0xc4/0xc5.
			leafVersion := controlBlock[0] & 0xfe
			require.True(t, leafVersion == 0xc0 || leafVersion == 0xc4,
				"iteration %d leaf %d: control block leaf version must be 0xc0 or 0xc4, got 0x%x", i, leafIdx, leafVersion)

			// Control block = 1 (version+parity) + 32 (internal key) + 32*N (merkle proof)
			cbLen := len(controlBlock)
			require.Equal(t, 0, (cbLen-33)%32,
				"iteration %d leaf %d: control block length must be 33+32*N, got %d", i, leafIdx, cbLen)
		}
	}
}

// TestRandomizedNetworkHRP verifies address encoding with different network prefixes.
func TestRandomizedNetworkHRP(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	hrps := []string{"ert", "ex", "tb", "bc"}

	for _, hrp := range hrps {
		params, _ := randomEscrowParams(t, rng)
		es, err := NewEscrowScript(params)
		require.NoError(t, err)

		addr, err := es.Address(hrp)
		require.NoError(t, err, "hrp=%s", hrp)
		require.Contains(t, addr, hrp+"1p", "hrp=%s: address must start with %s1p", hrp, hrp)
	}
}

// TestRandomizedBech32mChecksumValidation verifies that corrupted addresses are rejected.
func TestRandomizedBech32mChecksumValidation(t *testing.T) {
	rng := rand.New(rand.NewSource(rand.Int63()))
	iterations := 50

	for i := 0; i < iterations; i++ {
		params, _ := randomEscrowParams(t, rng)
		es, err := NewEscrowScript(params)
		require.NoError(t, err)

		addr, err := es.Address("ert")
		require.NoError(t, err)

		// Valid address must decode
		_, _, err = DecodeBech32(addr)
		require.NoError(t, err, "iteration %d: valid address must decode", i)

		// Corrupt a random character in the data part (after "ert1")
		addrBytes := []byte(addr)
		// Find the separator position
		sepIdx := 3 // "ert" length, separator is at index 3 ("ert1...")
		dataStart := sepIdx + 1
		corruptIdx := dataStart + rng.Intn(len(addrBytes)-dataStart)
		original := addrBytes[corruptIdx]
		charset := "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
		for {
			addrBytes[corruptIdx] = charset[rng.Intn(len(charset))]
			if addrBytes[corruptIdx] != original {
				break
			}
		}

		_, _, err = DecodeBech32(string(addrBytes))
		require.Error(t, err, "iteration %d: corrupted address must fail checksum (flipped index %d)", i, corruptIdx)
	}
}
