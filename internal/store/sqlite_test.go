package store

import (
	"crypto/rand"
	"testing"
	"time"

	arklib "github.com/arkade-os/arkd/pkg/ark-lib"
	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/btcsuite/btcd/btcec/v2"
)

func testMasterKey() []byte {
	key := make([]byte, 32)
	rand.Read(key)
	return key
}

func testStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	store, err := NewSQLiteStore(dbPath, testMasterKey())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testDeal(t *testing.T) *escrow.Deal {
	t.Helper()
	buyerKey, _ := btcec.NewPrivateKey()
	sellerKey, _ := btcec.NewPrivateKey()
	escrowPriv, _ := btcec.NewPrivateKey()
	serverKey, _ := btcec.NewPrivateKey()

	exitDelay := arklib.RelativeLocktime{Type: arklib.LocktimeTypeBlock, Value: 144}
	script := escrow.NewEscrowVtxoScript(
		buyerKey.PubKey(), sellerKey.PubKey(), escrowPriv.PubKey(), serverKey.PubKey(), exitDelay,
	)

	return &escrow.Deal{
		ID:           "test-deal-1",
		BuyerPubKey:  buyerKey.PubKey(),
		SellerPubKey: sellerKey.PubKey(),
		EscrowPubKey: escrowPriv.PubKey(),
		EscrowPriv:   escrowPriv,
		ServerPubKey: serverKey.PubKey(),
		Amount:       500000,
		Status:       escrow.DealCreated,
		Script:       script,
		CreatedAt:    time.Now().Truncate(time.Second),
		ExpiresAt:    time.Now().Add(24 * time.Hour).Truncate(time.Second),
	}
}

func TestCreateAndGetDeal(t *testing.T) {
	s := testStore(t)
	deal := testDeal(t)

	if err := s.CreateDeal(deal); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetDeal(deal.ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != deal.ID {
		t.Fatalf("id mismatch: %s vs %s", got.ID, deal.ID)
	}
	if got.Amount != deal.Amount {
		t.Fatalf("amount mismatch: %d vs %d", got.Amount, deal.Amount)
	}
	if got.Status != escrow.DealCreated {
		t.Fatalf("status mismatch: %s vs %s", got.Status, escrow.DealCreated)
	}
	if got.EscrowPriv == nil {
		t.Fatal("escrow private key should be decrypted")
	}
	if got.Script == nil {
		t.Fatal("script should be decoded")
	}
	if len(got.Script.Closures) != 4 {
		t.Fatalf("expected 4 closures, got %d", len(got.Script.Closures))
	}
}

func TestGetDealNotFound(t *testing.T) {
	s := testStore(t)

	_, err := s.GetDeal("nonexistent")
	if err == nil {
		t.Fatal("should return error for nonexistent deal")
	}
}

func TestUpdateStatus(t *testing.T) {
	s := testStore(t)
	deal := testDeal(t)
	s.CreateDeal(deal)

	if err := s.UpdateStatus(deal.ID, escrow.DealFunded); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetDeal(deal.ID)
	if got.Status != escrow.DealFunded {
		t.Fatalf("expected funded, got %s", got.Status)
	}
}

func TestUpdateStatusNotFound(t *testing.T) {
	s := testStore(t)

	err := s.UpdateStatus("nonexistent", escrow.DealFunded)
	if err == nil {
		t.Fatal("should return error for nonexistent deal")
	}
}

func TestUpdateStatusLifecycle(t *testing.T) {
	s := testStore(t)
	deal := testDeal(t)
	s.CreateDeal(deal)

	transitions := []escrow.DealStatus{
		escrow.DealFunded,
		escrow.DealReleased,
	}

	for _, status := range transitions {
		if err := s.UpdateStatus(deal.ID, status); err != nil {
			t.Fatalf("transition to %s: %v", status, err)
		}
		got, _ := s.GetDeal(deal.ID)
		if got.Status != status {
			t.Fatalf("expected %s, got %s", status, got.Status)
		}
	}
}

func TestSetVtxoOutpoint(t *testing.T) {
	s := testStore(t)
	deal := testDeal(t)
	s.CreateDeal(deal)

	txid := "aabbccddee112233445566778899aabbccddeeff00112233445566778899aabb"
	if err := s.SetVtxoOutpoint(deal.ID, txid, 1); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetDeal(deal.ID)
	if got.Status != escrow.DealFunded {
		t.Fatalf("expected funded after SetVtxoOutpoint, got %s", got.Status)
	}
	if got.VtxoOutpoint == nil {
		t.Fatal("vtxo outpoint should be set")
	}
	if got.VtxoOutpoint.Index != 1 {
		t.Fatalf("expected vout 1, got %d", got.VtxoOutpoint.Index)
	}
}

func TestListDeals(t *testing.T) {
	s := testStore(t)

	deal1 := testDeal(t)
	deal1.ID = "deal-1"
	deal2 := testDeal(t)
	deal2.ID = "deal-2"
	deal3 := testDeal(t)
	deal3.ID = "deal-3"

	s.CreateDeal(deal1)
	s.CreateDeal(deal2)
	s.CreateDeal(deal3)

	s.UpdateStatus("deal-2", escrow.DealFunded)

	created, err := s.ListDeals(escrow.DealCreated)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created deals, got %d", len(created))
	}

	funded, err := s.ListDeals(escrow.DealFunded)
	if err != nil {
		t.Fatal(err)
	}
	if len(funded) != 1 {
		t.Fatalf("expected 1 funded deal, got %d", len(funded))
	}
}

func TestDuplicateDealID(t *testing.T) {
	s := testStore(t)
	deal := testDeal(t)

	if err := s.CreateDeal(deal); err != nil {
		t.Fatal(err)
	}

	err := s.CreateDeal(deal)
	if err == nil {
		t.Fatal("should reject duplicate deal ID")
	}
}

func TestNewSQLiteStoreInvalidMasterKey(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"

	_, err := NewSQLiteStore(dbPath, []byte("short"))
	if err == nil {
		t.Fatal("should reject non-32-byte master key")
	}
}

func TestScriptRoundtripThroughDB(t *testing.T) {
	s := testStore(t)
	deal := testDeal(t)
	s.CreateDeal(deal)

	got, _ := s.GetDeal(deal.ID)

	origAddr, _ := escrow.EscrowAddress(deal.Script)
	gotAddr, _ := escrow.EscrowAddress(got.Script)

	if origAddr != gotAddr {
		t.Fatalf("script roundtrip changed address:\n  orig: %s\n  got:  %s", origAddr, gotAddr)
	}
}
