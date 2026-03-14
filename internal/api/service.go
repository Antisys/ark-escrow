package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	arklib "github.com/arkade-os/arkd/pkg/ark-lib"
	"github.com/Antisys/ark-escrow/internal/store"
	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type Service struct {
	store     *store.SQLiteStore
	serverKey *btcec.PublicKey // arkd signer pubkey
	mux       *http.ServeMux
}

func NewService(store *store.SQLiteStore, serverPubKeyHex string) (*Service, error) {
	b, err := hex.DecodeString(serverPubKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid server pubkey hex: %w", err)
	}
	serverKey, err := btcec.ParsePubKey(b)
	if err != nil {
		return nil, fmt.Errorf("invalid server pubkey: %w", err)
	}

	svc := &Service{
		store:     store,
		serverKey: serverKey,
		mux:       http.NewServeMux(),
	}
	svc.registerRoutes()
	return svc, nil
}

func (s *Service) Handler() http.Handler {
	return s.mux
}

func (s *Service) registerRoutes() {
	s.mux.HandleFunc("POST /escrow/v1/deals", s.handleCreateDeal)
	s.mux.HandleFunc("GET /escrow/v1/deals/{id}", s.handleGetDeal)
	s.mux.HandleFunc("POST /escrow/v1/deals/{id}/fund", s.handleFundDeal)
	s.mux.HandleFunc("POST /escrow/v1/deals/{id}/release", s.handleRelease)
	s.mux.HandleFunc("POST /escrow/v1/deals/{id}/refund", s.handleRefund)
}

// --- Request/Response types ---

type CreateDealRequest struct {
	BuyerPubKey   string `json:"buyer_pubkey"`
	SellerPubKey  string `json:"seller_pubkey"`
	Amount        uint64 `json:"amount"`
	TimeoutBlocks uint32 `json:"timeout_blocks"`
}

type CreateDealResponse struct {
	DealID       string   `json:"deal_id"`
	EscrowPubKey string   `json:"escrow_pubkey"`
	Tapscripts   []string `json:"tapscripts"`
	Address      string   `json:"address"`
}

type DealResponse struct {
	ID           string `json:"id"`
	BuyerPubKey  string `json:"buyer_pubkey"`
	SellerPubKey string `json:"seller_pubkey"`
	EscrowPubKey string `json:"escrow_pubkey"`
	Amount       uint64 `json:"amount"`
	Status       string `json:"status"`
	VtxoTxid     string `json:"vtxo_txid,omitempty"`
	VtxoVout     uint32 `json:"vtxo_vout,omitempty"`
	Address      string `json:"address"`
	CreatedAt    string `json:"created_at"`
	ExpiresAt    string `json:"expires_at"`
}

type FundDealRequest struct {
	VtxoTxid string `json:"vtxo_txid"`
	VtxoVout uint32 `json:"vtxo_vout"`
}

type ReleaseRequest struct {
	SignedPSBT string `json:"signed_psbt"` // Seller's partial signature
}

type RefundRequest struct {
	SignedPSBT string `json:"signed_psbt"` // Buyer's partial signature
}

type SignatureResponse struct {
	Signature string `json:"signature"`
	LeafIndex int    `json:"leaf_index"`
}

// --- Handlers ---

func (s *Service) handleCreateDeal(w http.ResponseWriter, r *http.Request) {
	var req CreateDealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Amount == 0 {
		httpError(w, http.StatusBadRequest, "amount must be > 0")
		return
	}
	if req.TimeoutBlocks == 0 {
		req.TimeoutBlocks = 144 // default ~1 day
	}

	buyerKey, err := parsePubKey(req.BuyerPubKey)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid buyer_pubkey: "+err.Error())
		return
	}
	sellerKey, err := parsePubKey(req.SellerPubKey)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid seller_pubkey: "+err.Error())
		return
	}

	escrowPriv, escrowPub, err := escrow.GenerateEscrowKeyPair()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "keygen failed")
		return
	}

	exitDelay := arklib.RelativeLocktime{
		Type:  arklib.LocktimeTypeBlock,
		Value: uint32(req.TimeoutBlocks),
	}

	vtxoScript := escrow.NewEscrowVtxoScript(buyerKey, sellerKey, escrowPub, s.serverKey, exitDelay)

	addr, err := escrow.EscrowAddress(vtxoScript)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "address generation failed")
		return
	}

	tapscripts, err := vtxoScript.Encode()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "script encoding failed")
		return
	}

	deal := &escrow.Deal{
		ID:           uuid.New().String(),
		BuyerPubKey:  buyerKey,
		SellerPubKey: sellerKey,
		EscrowPubKey: escrowPub,
		EscrowPriv:   escrowPriv,
		ServerPubKey: s.serverKey,
		Amount:       req.Amount,
		Status:       escrow.DealCreated,
		Script:       vtxoScript,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	if err := s.store.CreateDeal(deal); err != nil {
		log.Errorf("failed to store deal: %v", err)
		httpError(w, http.StatusInternalServerError, "failed to create deal")
		return
	}

	log.Infof("deal created: %s (amount: %d sats)", deal.ID, deal.Amount)

	jsonResponse(w, http.StatusCreated, CreateDealResponse{
		DealID:       deal.ID,
		EscrowPubKey: hex.EncodeToString(escrowPub.SerializeCompressed()),
		Tapscripts:   tapscripts,
		Address:      addr,
	})
}

func (s *Service) handleGetDeal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	deal, err := s.store.GetDeal(id)
	if err != nil {
		httpError(w, http.StatusNotFound, "deal not found")
		return
	}

	addr := ""
	if deal.Script != nil {
		addr, _ = escrow.EscrowAddress(deal.Script)
	}

	resp := DealResponse{
		ID:           deal.ID,
		BuyerPubKey:  hex.EncodeToString(deal.BuyerPubKey.SerializeCompressed()),
		SellerPubKey: hex.EncodeToString(deal.SellerPubKey.SerializeCompressed()),
		EscrowPubKey: hex.EncodeToString(deal.EscrowPubKey.SerializeCompressed()),
		Amount:       deal.Amount,
		Status:       string(deal.Status),
		Address:      addr,
		CreatedAt:    deal.CreatedAt.Format(time.RFC3339),
		ExpiresAt:    deal.ExpiresAt.Format(time.RFC3339),
	}

	if deal.VtxoOutpoint != nil {
		resp.VtxoTxid = deal.VtxoOutpoint.Hash.String()
		resp.VtxoVout = deal.VtxoOutpoint.Index
	}

	jsonResponse(w, http.StatusOK, resp)
}

func (s *Service) handleFundDeal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req FundDealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	deal, err := s.store.GetDeal(id)
	if err != nil {
		httpError(w, http.StatusNotFound, "deal not found")
		return
	}

	if deal.Status != escrow.DealCreated {
		httpError(w, http.StatusConflict, "deal already funded")
		return
	}

	if err := s.store.SetVtxoOutpoint(id, req.VtxoTxid, req.VtxoVout); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to update deal")
		return
	}

	log.Infof("deal funded: %s (vtxo: %s:%d)", id, req.VtxoTxid, req.VtxoVout)
	jsonResponse(w, http.StatusOK, map[string]string{"status": "funded"})
}

func (s *Service) handleRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	deal, err := s.store.GetDeal(id)
	if err != nil {
		httpError(w, http.StatusNotFound, "deal not found")
		return
	}

	if deal.Status != escrow.DealFunded {
		httpError(w, http.StatusConflict, fmt.Sprintf("deal status is %s, expected funded", deal.Status))
		return
	}

	// TODO: In production, verify the PSBT and add escrow signature.
	// For now, just update status.
	if err := s.store.UpdateStatus(id, escrow.DealReleased); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to update deal")
		return
	}

	log.Infof("deal released to seller: %s", id)
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":     "released",
		"leaf_index": escrow.LeafEscrowRelease,
		"message":    "escrow approved release to seller",
	})
}

func (s *Service) handleRefund(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	deal, err := s.store.GetDeal(id)
	if err != nil {
		httpError(w, http.StatusNotFound, "deal not found")
		return
	}

	if deal.Status != escrow.DealFunded {
		httpError(w, http.StatusConflict, fmt.Sprintf("deal status is %s, expected funded", deal.Status))
		return
	}

	if err := s.store.UpdateStatus(id, escrow.DealRefunded); err != nil {
		httpError(w, http.StatusInternalServerError, "failed to update deal")
		return
	}

	log.Infof("deal refunded to buyer: %s", id)
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":     "refunded",
		"leaf_index": escrow.LeafEscrowRefund,
		"message":    "escrow approved refund to buyer",
	})
}

// --- Helpers ---

func parsePubKey(hexStr string) (*btcec.PublicKey, error) {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex")
	}
	return btcec.ParsePubKey(b)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonResponse(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
