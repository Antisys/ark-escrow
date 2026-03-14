package config

import (
	"encoding/hex"
	"fmt"
	"os"
)

type Config struct {
	Port       int
	DBPath     string
	MasterKey  []byte // 32 bytes AES-256 key
	ArkdURL    string
	LogLevel   int
}

func Load() (*Config, error) {
	masterKeyHex := os.Getenv("ESCROW_MASTER_KEY")
	if masterKeyHex == "" {
		return nil, fmt.Errorf("ESCROW_MASTER_KEY env var required (64 hex chars)")
	}
	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(masterKey) != 32 {
		return nil, fmt.Errorf("ESCROW_MASTER_KEY must be 64 hex chars (32 bytes)")
	}

	port := 9090
	if p := os.Getenv("ESCROW_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	dbPath := os.Getenv("ESCROW_DB_PATH")
	if dbPath == "" {
		dbPath = "escrow.db"
	}

	arkdURL := os.Getenv("ESCROW_ARKD_URL")
	if arkdURL == "" {
		arkdURL = "http://localhost:7070"
	}

	return &Config{
		Port:      port,
		DBPath:    dbPath,
		MasterKey: masterKey,
		ArkdURL:   arkdURL,
	}, nil
}
