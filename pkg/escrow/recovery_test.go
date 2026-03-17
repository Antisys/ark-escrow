package escrow

import (
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/stretchr/testify/require"
)

func newTestDealWithKeys(t *testing.T) *Deal {
	t.Helper()

	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	secret, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	deal, err := NewDeal(
		"Test",
		50000,
		hex.EncodeToString(schnorr.SerializePubKey(sellerKey.PubKey())),
		hex.EncodeToString(schnorr.SerializePubKey(oracleKey.PubKey())),
		DefaultTimeoutBlocks,
	)
	require.NoError(t, err)

	deal.SellerPrivKey = hex.EncodeToString(sellerKey.Serialize())
	buyerPubHex := hex.EncodeToString(schnorr.SerializePubKey(buyerKey.PubKey()))
	require.NoError(t, deal.Join(buyerPubHex, hex.EncodeToString(secretHash[:])))
	deal.BuyerPrivKey = hex.EncodeToString(buyerKey.Serialize())
	deal.Secret = hex.EncodeToString(secret[:])
	require.NoError(t, deal.Fund("abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234", 1))

	return deal
}

func TestRecoveryKitForBuyer(t *testing.T) {
	deal := newTestDealWithKeys(t)

	kit, err := RecoveryKitForBuyer(deal)
	require.NoError(t, err)
	require.Equal(t, "buyer", kit.Role)
	require.Equal(t, deal.BuyerPrivKey, kit.PrivKey)
	require.Equal(t, deal.Secret, kit.Secret)
	require.Equal(t, deal.SellerPubKey, kit.SellerPubKey)
	require.Equal(t, deal.BuyerPubKey, kit.BuyerPubKey)
	require.Equal(t, deal.OraclePubKey, kit.OraclePubKey)
	require.Equal(t, deal.FundTxID, kit.FundTxID)
	require.Equal(t, deal.FundVout, kit.FundVout)
	require.Equal(t, deal.Amount, kit.Amount)
}

func TestRecoveryKitForSeller(t *testing.T) {
	deal := newTestDealWithKeys(t)

	kit, err := RecoveryKitForSeller(deal)
	require.NoError(t, err)
	require.Equal(t, "seller", kit.Role)
	require.Equal(t, deal.SellerPrivKey, kit.PrivKey)
	require.Empty(t, kit.Secret) // seller doesn't get the secret
}

func TestRecoveryKitEncodeDecode(t *testing.T) {
	deal := newTestDealWithKeys(t)

	kit, err := RecoveryKitForBuyer(deal)
	require.NoError(t, err)

	encoded, err := kit.Encode()
	require.NoError(t, err)
	require.Contains(t, encoded, "arkescrow")

	decoded, err := DecodeRecoveryKit(encoded)
	require.NoError(t, err)
	require.Equal(t, kit.Role, decoded.Role)
	require.Equal(t, kit.PrivKey, decoded.PrivKey)
	require.Equal(t, kit.Secret, decoded.Secret)
	require.Equal(t, kit.SellerPubKey, decoded.SellerPubKey)
	require.Equal(t, kit.BuyerPubKey, decoded.BuyerPubKey)
	require.Equal(t, kit.OraclePubKey, decoded.OraclePubKey)
	require.Equal(t, kit.SecretHash, decoded.SecretHash)
	require.Equal(t, kit.TimeoutBlocks, decoded.TimeoutBlocks)
	require.Equal(t, kit.FundTxID, decoded.FundTxID)
	require.Equal(t, kit.FundVout, decoded.FundVout)
	require.Equal(t, kit.Amount, decoded.Amount)
}

func TestRecoveryKitDecodeInvalid(t *testing.T) {
	_, err := DecodeRecoveryKit("garbage")
	require.Error(t, err)

	_, err = DecodeRecoveryKit("arkescrow" + "invalidbase58!!!")
	require.Error(t, err)
}

func TestRecoveryKitEscrowAddress(t *testing.T) {
	deal := newTestDealWithKeys(t)

	// Compute expected address from deal
	params, err := deal.EscrowParams()
	require.NoError(t, err)
	es, err := NewEscrowScript(*params)
	require.NoError(t, err)
	expectedAddr, err := es.Address("ert")
	require.NoError(t, err)

	// Kit should reconstruct the same address
	kit, err := RecoveryKitForBuyer(deal)
	require.NoError(t, err)
	kit.NetworkHRP = "ert"

	kitAddr, err := kit.EscrowAddress()
	require.NoError(t, err)
	require.Equal(t, expectedAddr, kitAddr)
}

func TestRecoveryKitRoundtripReconstructsAddress(t *testing.T) {
	deal := newTestDealWithKeys(t)

	kit, err := RecoveryKitForBuyer(deal)
	require.NoError(t, err)
	kit.NetworkHRP = "ert"

	// Encode → decode → reconstruct address
	encoded, err := kit.Encode()
	require.NoError(t, err)

	decoded, err := DecodeRecoveryKit(encoded)
	require.NoError(t, err)

	addr, err := decoded.EscrowAddress()
	require.NoError(t, err)

	// Should match original
	originalAddr, err := kit.EscrowAddress()
	require.NoError(t, err)
	require.Equal(t, originalAddr, addr)
}

func TestRecoveryKitValidate(t *testing.T) {
	deal := newTestDealWithKeys(t)
	kit, err := RecoveryKitForBuyer(deal)
	require.NoError(t, err)

	require.NoError(t, kit.Validate())

	// Missing privkey
	bad := *kit
	bad.PrivKey = ""
	require.Error(t, bad.Validate())

	// Bad role
	bad = *kit
	bad.Role = "oracle"
	require.Error(t, bad.Validate())

	// Missing pubkey
	bad = *kit
	bad.SellerPubKey = ""
	require.Error(t, bad.Validate())

	// Missing amount
	bad = *kit
	bad.Amount = 0
	require.Error(t, bad.Validate())
}

func TestRecoveryKitMissingPrivKey(t *testing.T) {
	deal := newTestDealWithKeys(t)
	deal.BuyerPrivKey = ""

	_, err := RecoveryKitForBuyer(deal)
	require.Error(t, err)
}
