package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/Antisys/ark-escrow/pkg/escrow/swap"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

type Server struct {
	store        escrow.Store
	lnd          *swap.LNDClient
	elementsd    *swap.ElementsdClient
	oraclePubKey string
	networkHRP   string
}

// --- Create Deal ---

type createDealRequest struct {
	Title   string `json:"title"`
	Amount  uint64 `json:"amount"`
	Timeout uint32 `json:"timeout"`
}

type createDealResponse struct {
	DealID       string           `json:"deal_id"`
	SellerPubKey string           `json:"seller_pubkey"`
	JoinToken    escrow.JoinToken `json:"join_token"`
}

func (s *Server) handleCreateDeal(w http.ResponseWriter, r *http.Request) {
	var req createDealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Amount == 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	if s.oraclePubKey == "" {
		writeError(w, http.StatusBadRequest, "oracle-pubkey not configured on server")
		return
	}
	if req.Timeout == 0 {
		req.Timeout = escrow.DefaultTimeoutBlocks
	}

	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate seller key: %v", err)
		return
	}
	sellerPubKey := hex.EncodeToString(schnorr.SerializePubKey(privKey.PubKey()))

	deal, err := escrow.NewDeal(req.Title, req.Amount, sellerPubKey, s.oraclePubKey, req.Timeout)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create deal: %v", err)
		return
	}
	deal.SellerPrivKey = hex.EncodeToString(privKey.Serialize())

	if err := s.store.Save(deal); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save deal: %v", err)
		return
	}

	writeJSON(w, http.StatusCreated, createDealResponse{
		DealID:       deal.ID,
		SellerPubKey: sellerPubKey,
		JoinToken:    deal.JoinToken(),
	})
}

// --- Join Deal ---

type joinDealRequest struct {
	Token escrow.JoinToken `json:"token"`
}

type joinDealResponse struct {
	DealID        string `json:"deal_id"`
	BuyerPubKey   string `json:"buyer_pubkey"`
	SecretHash    string `json:"secret_hash"`
	EscrowAddress string `json:"escrow_address"`
	RecoveryKit   string `json:"recovery_kit"`
}

func (s *Server) handleJoinDeal(w http.ResponseWriter, r *http.Request) {
	var req joinDealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}
	if req.Token.DealID == "" {
		writeError(w, http.StatusBadRequest, "token.deal_id is required")
		return
	}

	deal, err := s.store.Load(req.Token.DealID)
	if err != nil {
		deal = &escrow.Deal{
			ID:            req.Token.DealID,
			State:         escrow.DealStateCreated,
			Title:         req.Token.Title,
			Amount:        req.Token.Amount,
			SellerPubKey:  req.Token.SellerPubKey,
			OraclePubKey:  req.Token.OraclePubKey,
			TimeoutBlocks: req.Token.TimeoutBlocks,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		}
	}

	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate buyer key: %v", err)
		return
	}
	buyerPubKey := hex.EncodeToString(schnorr.SerializePubKey(privKey.PubKey()))

	secret, secretHash, err := escrow.GenerateSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate secret: %v", err)
		return
	}

	if err := deal.Join(buyerPubKey, hex.EncodeToString(secretHash[:])); err != nil {
		writeError(w, http.StatusConflict, "cannot join: %v", err)
		return
	}

	deal.BuyerPrivKey = hex.EncodeToString(privKey.Serialize())
	deal.Secret = hex.EncodeToString(secret[:])

	params, err := deal.EscrowParams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute escrow params: %v", err)
		return
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create escrow script: %v", err)
		return
	}
	addr, err := es.Address(s.networkHRP)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute address: %v", err)
		return
	}
	deal.SetEscrowAddress(addr)

	if err := s.store.Save(deal); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save deal: %v", err)
		return
	}

	resp := joinDealResponse{
		DealID:        deal.ID,
		BuyerPubKey:   buyerPubKey,
		SecretHash:    hex.EncodeToString(secretHash[:]),
		EscrowAddress: addr,
	}

	kit, err := escrow.RecoveryKitForBuyer(deal)
	if err == nil {
		kit.NetworkHRP = s.networkHRP
		encoded, err := kit.Encode()
		if err == nil {
			resp.RecoveryKit = encoded
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- List Deals ---

func (s *Server) handleListDeals(w http.ResponseWriter, r *http.Request) {
	deals, err := s.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deals: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, deals)
}

// --- Get Deal ---

func (s *Server) handleGetDeal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deal, err := s.store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "deal not found: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, deal)
}

// --- Fund Deal ---

type fundDealResponse struct {
	PaymentRequest string `json:"payment_request"`
	HTLCTxID       string `json:"htlc_txid"`
}

func (s *Server) handleFundDeal(w http.ResponseWriter, r *http.Request) {
	if s.lnd == nil || s.elementsd == nil {
		writeError(w, http.StatusServiceUnavailable, "LND and elementsd must be configured for funding")
		return
	}

	id := r.PathValue("id")
	deal, err := s.store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "deal not found: %v", err)
		return
	}
	if deal.EscrowAddress == "" {
		writeError(w, http.StatusBadRequest, "deal has no escrow address — join first")
		return
	}

	serviceKey, err := btcec.NewPrivateKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate service key: %v", err)
		return
	}

	ctx := context.Background()
	result, err := swap.Fund(ctx, swap.FundConfig{
		LND:            s.lnd,
		Elementsd:      s.elementsd,
		EscrowAddress:  deal.EscrowAddress,
		AmountSats:     deal.Amount,
		ServicePubKey:  serviceKey.PubKey(),
		ServicePrivKey: serviceKey,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "funding failed: %v", err)
		return
	}

	// Return the invoice immediately, settle in background
	writeJSON(w, http.StatusOK, fundDealResponse{
		PaymentRequest: result.PaymentRequest,
		HTLCTxID:       result.HTLCTxID,
	})

	// Background: wait for LN payment, claim HTLC → escrow, settle invoice, update deal
	go func() {
		bgCtx := context.Background()
		escrowTxID, err := swap.WaitForPaymentClaimAndSettle(
			bgCtx, s.lnd, s.elementsd, result,
			serviceKey, deal.EscrowAddress, deal.Amount, nil, 2*time.Second,
		)
		if err != nil {
			log.Printf("[fund] deal %s: settlement failed: %v", deal.ID, err)
			return
		}

		escrowVout, err := swap.FindVoutByAddress(bgCtx, s.elementsd, escrowTxID, deal.EscrowAddress)
		if err != nil {
			log.Printf("[fund] deal %s: funded (tx %s) but could not find vout: %v", deal.ID, escrowTxID, err)
			return
		}

		// Reload deal to avoid stale state
		deal, err = s.store.Load(id)
		if err != nil {
			log.Printf("[fund] deal %s: failed to reload deal: %v", deal.ID, err)
			return
		}
		if err := deal.Fund(escrowTxID, escrowVout); err != nil {
			log.Printf("[fund] deal %s: state transition failed: %v", deal.ID, err)
			return
		}
		if err := s.store.Save(deal); err != nil {
			log.Printf("[fund] deal %s: failed to save: %v", deal.ID, err)
			return
		}
		log.Printf("[fund] deal %s: funded, escrow tx %s", deal.ID, escrowTxID)
	}()
}

// --- Ship Deal ---

func (s *Server) handleShipDeal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deal, err := s.store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "deal not found: %v", err)
		return
	}
	if err := deal.Ship(); err != nil {
		writeError(w, http.StatusConflict, "cannot ship: %v", err)
		return
	}
	if err := s.store.Save(deal); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save deal: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, deal)
}

// --- Release Deal ---

type releaseDealRequest struct {
	SellerInvoice string `json:"seller_invoice"`
	DestAddress   string `json:"dest_address"`
}

type releaseDealResponse struct {
	ClaimTxID      string `json:"claim_txid"`
	PayoutPreimage string `json:"payout_preimage"`
}

func (s *Server) handleReleaseDeal(w http.ResponseWriter, r *http.Request) {
	if s.lnd == nil || s.elementsd == nil {
		writeError(w, http.StatusServiceUnavailable, "LND and elementsd must be configured")
		return
	}

	id := r.PathValue("id")
	deal, err := s.store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "deal not found: %v", err)
		return
	}

	var req releaseDealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}
	if req.SellerInvoice == "" {
		writeError(w, http.StatusBadRequest, "seller_invoice is required")
		return
	}

	if deal.SellerPrivKey == "" {
		writeError(w, http.StatusBadRequest, "seller private key not available")
		return
	}
	if deal.Secret == "" {
		writeError(w, http.StatusBadRequest, "buyer secret not available")
		return
	}

	sellerKey, err := parsePrivKey(deal.SellerPrivKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid seller key: %v", err)
		return
	}
	preimage, err := hex.DecodeString(deal.Secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid secret: %v", err)
		return
	}

	ctx := r.Context()

	destAddr := req.DestAddress
	if destAddr == "" {
		destAddr, err = s.elementsd.GetNewAddress(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get dest address: %v", err)
			return
		}
	}

	params, err := deal.EscrowParams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow params: %v", err)
		return
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow script: %v", err)
		return
	}

	claimResult, err := swap.ClaimEscrow(ctx, s.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     deal.FundTxID,
		FundVout:     deal.FundVout,
		Amount:       deal.Amount,
		Leaf:         swap.EscrowLeafRelease,
		SigningKeys:  []*btcec.PrivateKey{sellerKey},
		Preimage:     preimage,
		DestAddress:  destAddr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow claim failed: %v", err)
		return
	}

	payoutResult, err := swap.Payout(ctx, swap.PayoutConfig{
		LND:            s.lnd,
		PaymentRequest: req.SellerInvoice,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LN payout failed (escrow claimed in %s): %v", claimResult.TxID, err)
		return
	}

	if err := deal.Release(claimResult.TxID); err != nil {
		writeError(w, http.StatusConflict, "state transition failed: %v", err)
		return
	}
	if err := s.store.Save(deal); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, releaseDealResponse{
		ClaimTxID:      claimResult.TxID,
		PayoutPreimage: hex.EncodeToString(payoutResult.Preimage),
	})
}

// --- Refund Deal ---

type refundDealRequest struct {
	BuyerInvoice string `json:"buyer_invoice"`
	DestAddress  string `json:"dest_address"`
}

type refundDealResponse struct {
	ClaimTxID      string `json:"claim_txid"`
	PayoutPreimage string `json:"payout_preimage"`
}

func (s *Server) handleRefundDeal(w http.ResponseWriter, r *http.Request) {
	if s.lnd == nil || s.elementsd == nil {
		writeError(w, http.StatusServiceUnavailable, "LND and elementsd must be configured")
		return
	}

	id := r.PathValue("id")
	deal, err := s.store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "deal not found: %v", err)
		return
	}

	var req refundDealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}
	if req.BuyerInvoice == "" {
		writeError(w, http.StatusBadRequest, "buyer_invoice is required")
		return
	}

	if deal.BuyerPrivKey == "" {
		writeError(w, http.StatusBadRequest, "buyer private key not available")
		return
	}

	buyerKey, err := parsePrivKey(deal.BuyerPrivKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid buyer key: %v", err)
		return
	}

	ctx := r.Context()

	destAddr := req.DestAddress
	if destAddr == "" {
		destAddr, err = s.elementsd.GetNewAddress(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get dest address: %v", err)
			return
		}
	}

	params, err := deal.EscrowParams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow params: %v", err)
		return
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow script: %v", err)
		return
	}

	claimResult, err := swap.ClaimEscrow(ctx, s.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     deal.FundTxID,
		FundVout:     deal.FundVout,
		Amount:       deal.Amount,
		Leaf:         swap.EscrowLeafTimeout,
		SigningKeys:  []*btcec.PrivateKey{buyerKey},
		DestAddress:  destAddr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow claim failed: %v", err)
		return
	}

	payoutResult, err := swap.Payout(ctx, swap.PayoutConfig{
		LND:            s.lnd,
		PaymentRequest: req.BuyerInvoice,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LN payout failed (escrow claimed in %s): %v", claimResult.TxID, err)
		return
	}

	if err := deal.Refund(claimResult.TxID); err != nil {
		writeError(w, http.StatusConflict, "state transition failed: %v", err)
		return
	}
	if err := s.store.Save(deal); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, refundDealResponse{
		ClaimTxID:      claimResult.TxID,
		PayoutPreimage: hex.EncodeToString(payoutResult.Preimage),
	})
}

// --- Dispute Deal ---

type disputeDealRequest struct {
	Winner        string `json:"winner"`
	OraclePrivKey string `json:"oracle_privkey"`
	WinnerInvoice string `json:"winner_invoice"`
	DestAddress   string `json:"dest_address"`
}

type disputeDealResponse struct {
	ClaimTxID      string `json:"claim_txid"`
	PayoutPreimage string `json:"payout_preimage"`
}

func (s *Server) handleDisputeDeal(w http.ResponseWriter, r *http.Request) {
	if s.lnd == nil || s.elementsd == nil {
		writeError(w, http.StatusServiceUnavailable, "LND and elementsd must be configured")
		return
	}

	id := r.PathValue("id")
	deal, err := s.store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "deal not found: %v", err)
		return
	}

	var req disputeDealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}
	if req.Winner != "seller" && req.Winner != "buyer" {
		writeError(w, http.StatusBadRequest, "winner must be 'seller' or 'buyer'")
		return
	}
	if req.OraclePrivKey == "" {
		writeError(w, http.StatusBadRequest, "oracle_privkey is required")
		return
	}
	if req.WinnerInvoice == "" {
		writeError(w, http.StatusBadRequest, "winner_invoice is required")
		return
	}

	oracleKey, err := parsePrivKey(req.OraclePrivKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid oracle private key: %v", err)
		return
	}

	var winnerKey *btcec.PrivateKey
	var leaf swap.EscrowLeaf
	if req.Winner == "seller" {
		if deal.SellerPrivKey == "" {
			writeError(w, http.StatusBadRequest, "seller private key not available")
			return
		}
		winnerKey, err = parsePrivKey(deal.SellerPrivKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "invalid seller key: %v", err)
			return
		}
		leaf = swap.EscrowLeafDisputeSeller
	} else {
		if deal.BuyerPrivKey == "" {
			writeError(w, http.StatusBadRequest, "buyer private key not available")
			return
		}
		winnerKey, err = parsePrivKey(deal.BuyerPrivKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "invalid buyer key: %v", err)
			return
		}
		leaf = swap.EscrowLeafDisputeBuyer
	}

	ctx := r.Context()

	destAddr := req.DestAddress
	if destAddr == "" {
		destAddr, err = s.elementsd.GetNewAddress(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get dest address: %v", err)
			return
		}
	}

	params, err := deal.EscrowParams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow params: %v", err)
		return
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow script: %v", err)
		return
	}

	claimResult, err := swap.ClaimEscrow(ctx, s.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     deal.FundTxID,
		FundVout:     deal.FundVout,
		Amount:       deal.Amount,
		Leaf:         leaf,
		SigningKeys:  []*btcec.PrivateKey{oracleKey, winnerKey},
		DestAddress:  destAddr,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "escrow claim failed: %v", err)
		return
	}

	payoutResult, err := swap.Payout(ctx, swap.PayoutConfig{
		LND:            s.lnd,
		PaymentRequest: req.WinnerInvoice,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LN payout failed (escrow claimed in %s): %v", claimResult.TxID, err)
		return
	}

	if err := deal.Dispute(claimResult.TxID); err != nil {
		writeError(w, http.StatusConflict, "state transition failed: %v", err)
		return
	}
	if err := s.store.Save(deal); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, disputeDealResponse{
		ClaimTxID:      claimResult.TxID,
		PayoutPreimage: hex.EncodeToString(payoutResult.Preimage),
	})
}

// --- Recovery Kit ---

func (s *Server) handleRecoveryKit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deal, err := s.store.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "deal not found: %v", err)
		return
	}

	role := r.URL.Query().Get("role")
	if role != "buyer" && role != "seller" {
		writeError(w, http.StatusBadRequest, "query param role must be 'buyer' or 'seller'")
		return
	}

	var kit *escrow.RecoveryKit
	if role == "buyer" {
		kit, err = escrow.RecoveryKitForBuyer(deal)
	} else {
		kit, err = escrow.RecoveryKitForSeller(deal)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot create recovery kit: %v", err)
		return
	}
	kit.NetworkHRP = s.networkHRP

	format := r.URL.Query().Get("format")
	if format == "encoded" {
		encoded, err := kit.Encode()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode kit: %v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"recovery_kit": encoded})
	} else {
		writeJSON(w, http.StatusOK, kit)
	}
}

// --- Recover ---

type recoverRequest struct {
	Kit         string `json:"kit"`
	DestAddress string `json:"dest_address"`
	Secret      string `json:"secret"`
}

type recoverResponse struct {
	ClaimTxID   string `json:"claim_txid"`
	DestAddress string `json:"dest_address"`
}

func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	if s.elementsd == nil {
		writeError(w, http.StatusServiceUnavailable, "elementsd must be configured for recovery")
		return
	}

	var req recoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: %v", err)
		return
	}
	if req.Kit == "" {
		writeError(w, http.StatusBadRequest, "kit is required")
		return
	}
	if req.DestAddress == "" {
		writeError(w, http.StatusBadRequest, "dest_address is required")
		return
	}

	kit, err := escrow.DecodeRecoveryKit(req.Kit)
	if err != nil {
		// Try as JSON
		kit = &escrow.RecoveryKit{}
		if jsonErr := json.Unmarshal([]byte(req.Kit), kit); jsonErr != nil {
			writeError(w, http.StatusBadRequest, "invalid recovery kit: %v", err)
			return
		}
	}
	if err := kit.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid recovery kit: %v", err)
		return
	}
	if kit.FundTxID == "" {
		writeError(w, http.StatusBadRequest, "recovery kit has no funding outpoint")
		return
	}

	ctx := r.Context()

	params, err := kit.EscrowParams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reconstruct escrow params: %v", err)
		return
	}
	es, err := escrow.NewEscrowScript(*params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reconstruct escrow script: %v", err)
		return
	}

	signingKey, err := parsePrivKey(kit.PrivKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid private key in kit: %v", err)
		return
	}

	var leaf swap.EscrowLeaf
	var preimage []byte

	switch kit.Role {
	case "buyer":
		leaf = swap.EscrowLeafTimeout
	case "seller":
		secretHex := req.Secret
		if secretHex == "" && kit.Secret != "" {
			secretHex = kit.Secret
		}
		if secretHex == "" {
			writeError(w, http.StatusBadRequest, "seller needs secret to claim via release leaf")
			return
		}
		preimage, err = hex.DecodeString(secretHex)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid secret: %v", err)
			return
		}
		leaf = swap.EscrowLeafRelease
	}

	out, err := s.elementsd.GetTxOut(ctx, kit.FundTxID, kit.FundVout)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check UTXO: %v", err)
		return
	}
	if out == nil {
		writeError(w, http.StatusGone, "escrow UTXO %s:%d has already been spent", kit.FundTxID, kit.FundVout)
		return
	}

	claimResult, err := swap.ClaimEscrow(ctx, s.elementsd, swap.ClaimEscrowConfig{
		EscrowScript: es,
		FundTxID:     kit.FundTxID,
		FundVout:     kit.FundVout,
		Amount:       kit.Amount,
		Leaf:         leaf,
		SigningKeys:  []*btcec.PrivateKey{signingKey},
		Preimage:     preimage,
		DestAddress:  req.DestAddress,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "claim failed: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, recoverResponse{
		ClaimTxID:   claimResult.TxID,
		DestAddress: req.DestAddress,
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error": fmt.Sprintf(format, args...),
	})
}

func parsePrivKey(hexKey string) (*btcec.PrivateKey, error) {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("expected 32 bytes, got %d", len(keyBytes))
	}
	privKey, _ := btcec.PrivKeyFromBytes(keyBytes)
	return privKey, nil
}
