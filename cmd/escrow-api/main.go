package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/Antisys/ark-escrow/pkg/escrow/swap"
)

func main() {
	generateKey := flag.Bool("generate-key", false, "Generate a random API key and exit")
	listenAddr := flag.String("listen", envOrDefault("ESCROW_LISTEN", ":8080"), "HTTP listen address")
	apiKey := flag.String("api-key", envOrDefault("ESCROW_API_KEY", ""), "API key for authentication (required)")
	tlsCert := flag.String("tls-cert", envOrDefault("ESCROW_TLS_CERT", ""), "TLS certificate file (enables HTTPS)")
	tlsKey := flag.String("tls-key", envOrDefault("ESCROW_TLS_KEY", ""), "TLS private key file")
	dataDir := flag.String("datadir", envOrDefault("ESCROW_DATADIR", defaultDataDir()), "Data directory for deal storage")
	lndURL := flag.String("lnd-url", envOrDefault("ESCROW_LND_URL", "https://localhost:18080"), "LND REST URL")
	lndMacaroon := flag.String("lnd-macaroon", envOrDefault("ESCROW_LND_MACAROON", ""), "LND admin macaroon (hex)")
	elementsdURL := flag.String("elementsd-url", envOrDefault("ESCROW_ELEMENTSD_URL", ""), "elementsd RPC URL")
	oraclePubKey := flag.String("oracle-pubkey", envOrDefault("ESCROW_ORACLE_PUBKEY", ""), "Oracle public key (hex)")
	networkHRP := flag.String("network-hrp", envOrDefault("ESCROW_NETWORK_HRP", "ert"), "Bech32 HRP (ert=regtest, ex=liquid)")
	flag.Parse()

	if *generateKey {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("Failed to generate key: %v", err)
		}
		fmt.Println(hex.EncodeToString(b))
		return
	}

	if *apiKey == "" {
		log.Fatal("--api-key or ESCROW_API_KEY is required")
	}

	store, err := escrow.NewFileStore(*dataDir)
	if err != nil {
		log.Fatalf("Failed to create store: %v", err)
	}

	var lnd *swap.LNDClient
	if *lndMacaroon != "" {
		lnd = swap.NewLNDClient(*lndURL, *lndMacaroon)
	}

	var elementsd *swap.ElementsdClient
	if *elementsdURL != "" {
		elementsd, err = swap.NewElementsdClient(*elementsdURL)
		if err != nil {
			log.Fatalf("Failed to create elementsd client: %v", err)
		}
	}

	srv := &Server{
		store:        store,
		lnd:          lnd,
		elementsd:    elementsd,
		oraclePubKey: *oraclePubKey,
		networkHRP:   *networkHRP,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/deals", srv.handleCreateDeal)
	mux.HandleFunc("POST /api/deals/join", srv.handleJoinDeal)
	mux.HandleFunc("GET /api/deals", srv.handleListDeals)
	mux.HandleFunc("GET /api/deals/{id}", srv.handleGetDeal)
	mux.HandleFunc("POST /api/deals/{id}/fund", srv.handleFundDeal)
	mux.HandleFunc("POST /api/deals/{id}/ship", srv.handleShipDeal)
	mux.HandleFunc("POST /api/deals/{id}/release", srv.handleReleaseDeal)
	mux.HandleFunc("POST /api/deals/{id}/refund", srv.handleRefundDeal)
	mux.HandleFunc("POST /api/deals/{id}/dispute", srv.handleDisputeDeal)
	mux.HandleFunc("GET /api/deals/{id}/recoverykit", srv.handleRecoveryKit)
	mux.HandleFunc("POST /api/recover", srv.handleRecover)

	handler := requireAPIKey(*apiKey, mux)

	if *tlsCert != "" && *tlsKey != "" {
		fmt.Printf("Escrow API listening on %s (HTTPS)\n", *listenAddr)
		if err := http.ListenAndServeTLS(*listenAddr, *tlsCert, *tlsKey, handler); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	} else {
		fmt.Printf("Escrow API listening on %s (HTTP)\n", *listenAddr)
		if err := http.ListenAndServe(*listenAddr, handler); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ark-escrow"
	}
	return home + "/.ark-escrow/deals"
}

// requireAPIKey returns middleware that rejects requests without a valid
// Authorization: Bearer <key> header. Uses constant-time comparison to
// prevent timing attacks on the key.
func requireAPIKey(key string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"missing or invalid Authorization header"}`))
			return
		}
		token := auth[7:]
		if subtle.ConstantTimeCompare([]byte(token), []byte(key)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid API key"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
