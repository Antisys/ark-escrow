package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Antisys/ark-escrow/internal/store"
	"github.com/btcsuite/btcd/btcec/v2"
)

func testService(t *testing.T) *Service {
	t.Helper()
	masterKey := make([]byte, 32)
	rand.Read(masterKey)

	dbPath := t.TempDir() + "/test.db"
	s, err := store.NewSQLiteStore(dbPath, masterKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	serverKey, _ := btcec.NewPrivateKey()
	serverPubHex := pubKeyHex(serverKey.PubKey())

	svc, err := NewService(s, serverPubHex)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func pubKeyHex(key *btcec.PublicKey) string {
	return bytesToHex(key.SerializeCompressed())
}

func bytesToHex(b []byte) string {
	const hextable = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = hextable[v>>4]
		dst[i*2+1] = hextable[v&0x0f]
	}
	return string(dst)
}

func doRequest(svc *Service, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, req)
	return w
}

func TestCreateDeal_HappyPath(t *testing.T) {
	svc := testService(t)
	buyerKey, _ := btcec.NewPrivateKey()
	sellerKey, _ := btcec.NewPrivateKey()

	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:   pubKeyHex(buyerKey.PubKey()),
		SellerPubKey:  pubKeyHex(sellerKey.PubKey()),
		Amount:        500000,
		TimeoutBlocks: 144,
	})

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp CreateDealResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.DealID == "" {
		t.Fatal("deal_id must not be empty")
	}
	if resp.EscrowPubKey == "" {
		t.Fatal("escrow_pubkey must not be empty")
	}
	if len(resp.Tapscripts) != 4 {
		t.Fatalf("expected 4 tapscripts, got %d", len(resp.Tapscripts))
	}
	if resp.Address == "" {
		t.Fatal("address must not be empty")
	}
	if resp.Address[:4] != "5120" {
		t.Fatalf("address must start with 5120 (P2TR), got %s", resp.Address[:4])
	}
}

func TestCreateDeal_ZeroAmount(t *testing.T) {
	svc := testService(t)
	buyerKey, _ := btcec.NewPrivateKey()
	sellerKey, _ := btcec.NewPrivateKey()

	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:  pubKeyHex(buyerKey.PubKey()),
		SellerPubKey: pubKeyHex(sellerKey.PubKey()),
		Amount:       0,
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateDeal_InvalidBuyerKey(t *testing.T) {
	svc := testService(t)
	sellerKey, _ := btcec.NewPrivateKey()

	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:  "not-a-valid-hex-key",
		SellerPubKey: pubKeyHex(sellerKey.PubKey()),
		Amount:       100000,
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateDeal_InvalidSellerKey(t *testing.T) {
	svc := testService(t)
	buyerKey, _ := btcec.NewPrivateKey()

	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:  pubKeyHex(buyerKey.PubKey()),
		SellerPubKey: "deadbeef",
		Amount:       100000,
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateDeal_EmptyBody(t *testing.T) {
	svc := testService(t)

	req := httptest.NewRequest("POST", "/escrow/v1/deals", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateDeal_DefaultTimeout(t *testing.T) {
	svc := testService(t)
	buyerKey, _ := btcec.NewPrivateKey()
	sellerKey, _ := btcec.NewPrivateKey()

	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:  pubKeyHex(buyerKey.PubKey()),
		SellerPubKey: pubKeyHex(sellerKey.PubKey()),
		Amount:       100000,
		// TimeoutBlocks omitted → should default to 144
	})

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetDeal_HappyPath(t *testing.T) {
	svc := testService(t)
	buyerKey, _ := btcec.NewPrivateKey()
	sellerKey, _ := btcec.NewPrivateKey()

	// Create deal first
	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:  pubKeyHex(buyerKey.PubKey()),
		SellerPubKey: pubKeyHex(sellerKey.PubKey()),
		Amount:       500000,
	})
	var createResp CreateDealResponse
	json.NewDecoder(w.Body).Decode(&createResp)

	// Get deal
	w = doRequest(svc, "GET", "/escrow/v1/deals/"+createResp.DealID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp DealResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ID != createResp.DealID {
		t.Fatalf("id mismatch")
	}
	if resp.Amount != 500000 {
		t.Fatalf("amount mismatch: %d", resp.Amount)
	}
	if resp.Status != "created" {
		t.Fatalf("expected status created, got %s", resp.Status)
	}
}

func TestGetDeal_NotFound(t *testing.T) {
	svc := testService(t)

	w := doRequest(svc, "GET", "/escrow/v1/deals/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func createTestDeal(t *testing.T, svc *Service) string {
	t.Helper()
	buyerKey, _ := btcec.NewPrivateKey()
	sellerKey, _ := btcec.NewPrivateKey()

	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:  pubKeyHex(buyerKey.PubKey()),
		SellerPubKey: pubKeyHex(sellerKey.PubKey()),
		Amount:       500000,
	})
	var resp CreateDealResponse
	json.NewDecoder(w.Body).Decode(&resp)
	return resp.DealID
}

func TestFundDeal_HappyPath(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{
		VtxoTxid: "aabbccddee112233445566778899aabbccddeeff00112233445566778899aabb",
		VtxoVout: 0,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify status changed
	w = doRequest(svc, "GET", "/escrow/v1/deals/"+dealID, nil)
	var resp DealResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "funded" {
		t.Fatalf("expected funded, got %s", resp.Status)
	}
}

func TestFundDeal_NotFound(t *testing.T) {
	svc := testService(t)

	w := doRequest(svc, "POST", "/escrow/v1/deals/nonexistent/fund", FundDealRequest{
		VtxoTxid: "aabb", VtxoVout: 0,
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestFundDeal_AlreadyFunded(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	// Fund once
	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{
		VtxoTxid: "aabb", VtxoVout: 0,
	})

	// Fund again
	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{
		VtxoTxid: "ccdd", VtxoVout: 1,
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d", w.Code)
	}
}

func TestRelease_HappyPath(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	// Fund first
	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{
		VtxoTxid: "aabb", VtxoVout: 0,
	})

	// Release
	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/release", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify status
	w = doRequest(svc, "GET", "/escrow/v1/deals/"+dealID, nil)
	var resp DealResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "released" {
		t.Fatalf("expected released, got %s", resp.Status)
	}
}

func TestRelease_NotFunded(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/release", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestRelease_AlreadyReleased(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{VtxoTxid: "aa", VtxoVout: 0})
	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/release", nil)

	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/release", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestRefund_HappyPath(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{VtxoTxid: "aa", VtxoVout: 0})

	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/refund", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = doRequest(svc, "GET", "/escrow/v1/deals/"+dealID, nil)
	var resp DealResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "refunded" {
		t.Fatalf("expected refunded, got %s", resp.Status)
	}
}

func TestRefund_NotFunded(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/refund", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestRefund_AfterRelease(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{VtxoTxid: "aa", VtxoVout: 0})
	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/release", nil)

	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/refund", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 (can't refund after release), got %d", w.Code)
	}
}

func TestFullLifecycle_Release(t *testing.T) {
	svc := testService(t)
	buyerKey, _ := btcec.NewPrivateKey()
	sellerKey, _ := btcec.NewPrivateKey()

	// 1. Create
	w := doRequest(svc, "POST", "/escrow/v1/deals", CreateDealRequest{
		BuyerPubKey:  pubKeyHex(buyerKey.PubKey()),
		SellerPubKey: pubKeyHex(sellerKey.PubKey()),
		Amount:       1000000,
	})
	if w.Code != 201 {
		t.Fatalf("create: %d", w.Code)
	}
	var cr CreateDealResponse
	json.NewDecoder(w.Body).Decode(&cr)

	// 2. Get → created
	w = doRequest(svc, "GET", "/escrow/v1/deals/"+cr.DealID, nil)
	var dr DealResponse
	json.NewDecoder(w.Body).Decode(&dr)
	if dr.Status != "created" {
		t.Fatalf("step 2: expected created, got %s", dr.Status)
	}

	// 3. Fund
	w = doRequest(svc, "POST", "/escrow/v1/deals/"+cr.DealID+"/fund", FundDealRequest{
		VtxoTxid: "1122334455667788990011223344556677889900112233445566778899001122",
		VtxoVout: 0,
	})
	if w.Code != 200 {
		t.Fatalf("fund: %d", w.Code)
	}

	// 4. Get → funded
	w = doRequest(svc, "GET", "/escrow/v1/deals/"+cr.DealID, nil)
	json.NewDecoder(w.Body).Decode(&dr)
	if dr.Status != "funded" {
		t.Fatalf("step 4: expected funded, got %s", dr.Status)
	}

	// 5. Release
	w = doRequest(svc, "POST", "/escrow/v1/deals/"+cr.DealID+"/release", nil)
	if w.Code != 200 {
		t.Fatalf("release: %d", w.Code)
	}

	// 6. Get → released
	w = doRequest(svc, "GET", "/escrow/v1/deals/"+cr.DealID, nil)
	json.NewDecoder(w.Body).Decode(&dr)
	if dr.Status != "released" {
		t.Fatalf("step 6: expected released, got %s", dr.Status)
	}
}

func TestFullLifecycle_Refund(t *testing.T) {
	svc := testService(t)
	dealID := createTestDeal(t, svc)

	doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/fund", FundDealRequest{VtxoTxid: "aa", VtxoVout: 0})

	w := doRequest(svc, "POST", "/escrow/v1/deals/"+dealID+"/refund", nil)
	if w.Code != 200 {
		t.Fatalf("refund: %d", w.Code)
	}

	w = doRequest(svc, "GET", "/escrow/v1/deals/"+dealID, nil)
	var dr DealResponse
	json.NewDecoder(w.Body).Decode(&dr)
	if dr.Status != "refunded" {
		t.Fatalf("expected refunded, got %s", dr.Status)
	}
}
