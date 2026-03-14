package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Antisys/ark-escrow/internal/api"
	"github.com/Antisys/ark-escrow/internal/config"
	"github.com/Antisys/ark-escrow/internal/store"
	log "github.com/sirupsen/logrus"
)

var Version = "dev"

func main() {
	log.SetLevel(log.DebugLevel)
	log.Infof("ark-escrow agent %s starting...", Version)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	// Fetch server pubkey from arkd
	serverPubKey, err := fetchServerPubKey(cfg.ArkdURL)
	if err != nil {
		log.Fatalf("failed to get arkd server info: %v", err)
	}
	log.Infof("connected to arkd at %s (signer: %s...)", cfg.ArkdURL, serverPubKey[:16])

	db, err := store.NewSQLiteStore(cfg.DBPath, cfg.MasterKey)
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer db.Close()

	svc, err := api.NewService(db, serverPubKey)
	if err != nil {
		log.Fatalf("service error: %v", err)
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: svc.Handler(),
	}

	go func() {
		log.Infof("escrow agent listening on :%d", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	<-sigChan

	log.Info("shutting down...")
	server.Close()
}

func fetchServerPubKey(arkdURL string) (string, error) {
	resp, err := http.Get(arkdURL + "/v1/info")
	if err != nil {
		return "", fmt.Errorf("failed to reach arkd: %w", err)
	}
	defer resp.Body.Close()

	var info struct {
		SignerPubkey string `json:"signerPubkey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("failed to parse info: %w", err)
	}
	if info.SignerPubkey == "" {
		return "", fmt.Errorf("empty signer pubkey from arkd")
	}
	return info.SignerPubkey, nil
}
