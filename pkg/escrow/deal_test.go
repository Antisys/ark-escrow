package escrow

import (
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/stretchr/testify/require"
)

func newTestDeal(t *testing.T) *Deal {
	t.Helper()
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)

	deal, err := NewDeal(
		"Test Widget",
		50000,
		hex.EncodeToString(schnorr.SerializePubKey(sellerKey.PubKey())),
		hex.EncodeToString(schnorr.SerializePubKey(oracleKey.PubKey())),
		DefaultTimeoutBlocks,
	)
	require.NoError(t, err)
	return deal
}

func TestNewDeal(t *testing.T) {
	deal := newTestDeal(t)
	require.Equal(t, DealStateCreated, deal.State)
	require.NotEmpty(t, deal.ID)
	require.Equal(t, "Test Widget", deal.Title)
	require.Equal(t, uint64(50000), deal.Amount)
	require.False(t, deal.CreatedAt.IsZero())
}

func TestDealJoin(t *testing.T) {
	deal := newTestDeal(t)

	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerPub := hex.EncodeToString(schnorr.SerializePubKey(buyerKey.PubKey()))

	_, secretHash, err := GenerateSecret()
	require.NoError(t, err)
	hashHex := hex.EncodeToString(secretHash[:])

	err = deal.Join(buyerPub, hashHex)
	require.NoError(t, err)
	require.Equal(t, DealStateJoined, deal.State)
	require.Equal(t, buyerPub, deal.BuyerPubKey)
	require.Equal(t, hashHex, deal.SecretHash)
}

func TestDealJoinInvalidState(t *testing.T) {
	deal := newTestDeal(t)
	deal.State = DealStateFunded

	err := deal.Join("pubkey", "hash")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot join deal in state FUNDED")
}

func TestDealFund(t *testing.T) {
	deal := newTestDeal(t)
	deal.State = DealStateJoined

	err := deal.Fund("txid123", 0)
	require.NoError(t, err)
	require.Equal(t, DealStateFunded, deal.State)
	require.Equal(t, "txid123", deal.FundTxID)
}

func TestDealFundInvalidState(t *testing.T) {
	deal := newTestDeal(t)
	// Still CREATED, not JOINED
	err := deal.Fund("txid123", 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot fund deal in state CREATED")
}

func TestDealShip(t *testing.T) {
	deal := newTestDeal(t)
	deal.State = DealStateFunded

	err := deal.Ship()
	require.NoError(t, err)
	require.Equal(t, DealStateShipped, deal.State)
}

func TestDealShipInvalidState(t *testing.T) {
	deal := newTestDeal(t)
	deal.State = DealStateJoined

	err := deal.Ship()
	require.Error(t, err)
}

func TestDealRelease(t *testing.T) {
	for _, fromState := range []DealState{DealStateFunded, DealStateShipped} {
		t.Run(string(fromState), func(t *testing.T) {
			deal := newTestDeal(t)
			deal.State = fromState

			err := deal.Release("claim123")
			require.NoError(t, err)
			require.Equal(t, DealStateReleased, deal.State)
			require.Equal(t, "claim123", deal.ClaimTxID)
		})
	}
}

func TestDealReleaseInvalidState(t *testing.T) {
	for _, fromState := range []DealState{DealStateCreated, DealStateJoined, DealStateReleased, DealStateRefunded} {
		t.Run(string(fromState), func(t *testing.T) {
			deal := newTestDeal(t)
			deal.State = fromState

			err := deal.Release("claim123")
			require.Error(t, err)
		})
	}
}

func TestDealRefund(t *testing.T) {
	for _, fromState := range []DealState{DealStateFunded, DealStateShipped} {
		t.Run(string(fromState), func(t *testing.T) {
			deal := newTestDeal(t)
			deal.State = fromState

			err := deal.Refund("refund123")
			require.NoError(t, err)
			require.Equal(t, DealStateRefunded, deal.State)
			require.Equal(t, "refund123", deal.ClaimTxID)
		})
	}
}

func TestDealRefundInvalidState(t *testing.T) {
	deal := newTestDeal(t)
	deal.State = DealStateReleased

	err := deal.Refund("refund123")
	require.Error(t, err)
}

func TestDealDispute(t *testing.T) {
	deal := newTestDeal(t)
	deal.State = DealStateFunded

	err := deal.Dispute("dispute123")
	require.NoError(t, err)
	require.Equal(t, DealStateDisputed, deal.State)
}

func TestDealDisputeInvalidState(t *testing.T) {
	deal := newTestDeal(t)
	deal.State = DealStateCreated

	err := deal.Dispute("dispute123")
	require.Error(t, err)
}

func TestDealEscrowParams(t *testing.T) {
	sellerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	buyerKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	oracleKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	_, secretHash, err := GenerateSecret()
	require.NoError(t, err)

	deal := &Deal{
		SellerPubKey:  hex.EncodeToString(schnorr.SerializePubKey(sellerKey.PubKey())),
		BuyerPubKey:   hex.EncodeToString(schnorr.SerializePubKey(buyerKey.PubKey())),
		OraclePubKey:  hex.EncodeToString(schnorr.SerializePubKey(oracleKey.PubKey())),
		SecretHash:    hex.EncodeToString(secretHash[:]),
		TimeoutBlocks: DefaultTimeoutBlocks,
	}

	params, err := deal.EscrowParams()
	require.NoError(t, err)
	require.NotNil(t, params.SellerPubKey)
	require.NotNil(t, params.BuyerPubKey)
	require.NotNil(t, params.OraclePubKey)
	require.Equal(t, secretHash, params.SecretHash)
}

func TestDealEscrowParamsMissingFields(t *testing.T) {
	deal := &Deal{}
	_, err := deal.EscrowParams()
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required keys")
}

func TestDealJoinToken(t *testing.T) {
	deal := newTestDeal(t)
	token := deal.JoinToken()

	require.Equal(t, deal.ID, token.DealID)
	require.Equal(t, deal.Title, token.Title)
	require.Equal(t, deal.Amount, token.Amount)
	require.Equal(t, deal.SellerPubKey, token.SellerPubKey)
	require.Equal(t, deal.OraclePubKey, token.OraclePubKey)
	require.Equal(t, deal.TimeoutBlocks, token.TimeoutBlocks)
}

func TestDealUpdatedAtChanges(t *testing.T) {
	deal := newTestDeal(t)
	created := deal.UpdatedAt

	buyerKey, _ := btcec.NewPrivateKey()
	buyerPub := hex.EncodeToString(schnorr.SerializePubKey(buyerKey.PubKey()))
	_, secretHash, _ := GenerateSecret()

	_ = deal.Join(buyerPub, hex.EncodeToString(secretHash[:]))
	require.False(t, deal.UpdatedAt.Before(created))
}
