package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/Antisys/ark-escrow/pkg/escrow/swap"
)

func main() {
	listenAddr := flag.String("listen", envOrDefault("ESCROW_LISTEN", ":8080"), "HTTP listen address")
	dataDir := flag.String("datadir", envOrDefault("ESCROW_DATADIR", defaultDataDir()), "Data directory for deal storage")
	lndURL := flag.String("lnd-url", envOrDefault("ESCROW_LND_URL", "https://localhost:18080"), "LND REST URL")
	lndMacaroon := flag.String("lnd-macaroon", envOrDefault("ESCROW_LND_MACAROON", ""), "LND admin macaroon (hex)")
	elementsdURL := flag.String("elementsd-url", envOrDefault("ESCROW_ELEMENTSD_URL", ""), "elementsd RPC URL")
	oraclePubKey := flag.String("oracle-pubkey", envOrDefault("ESCROW_ORACLE_PUBKEY", ""), "Oracle public key (hex)")
	networkHRP := flag.String("network-hrp", envOrDefault("ESCROW_NETWORK_HRP", "ert"), "Bech32 HRP (ert=regtest, ex=liquid)")
	flag.Parse()

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

	fmt.Printf("Escrow API listening on %s\n", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
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
