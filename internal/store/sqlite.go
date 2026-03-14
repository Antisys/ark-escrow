package store

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Antisys/ark-escrow/pkg/escrow"
	"github.com/arkade-os/arkd/pkg/ark-lib/script"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db        *sql.DB
	masterKey []byte // AES-256 key for encrypting escrow private keys
}

func NewSQLiteStore(dbPath string, masterKey []byte) (*SQLiteStore, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return &SQLiteStore{db: db, masterKey: masterKey}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS deals (
			id TEXT PRIMARY KEY,
			buyer_pubkey TEXT NOT NULL,
			seller_pubkey TEXT NOT NULL,
			escrow_pubkey TEXT NOT NULL,
			escrow_privkey_enc BLOB NOT NULL,
			server_pubkey TEXT NOT NULL,
			amount INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'created',
			vtxo_txid TEXT,
			vtxo_vout INTEGER,
			scripts TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP
		)
	`)
	return err
}

func (s *SQLiteStore) CreateDeal(deal *escrow.Deal) error {
	encPriv, err := escrow.EncryptPrivateKey(deal.EscrowPriv, s.masterKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt key: %w", err)
	}

	var scriptsHex string
	if deal.Script != nil {
		encoded, err := deal.Script.Encode()
		if err != nil {
			return fmt.Errorf("failed to encode scripts: %w", err)
		}
		for i, e := range encoded {
			if i > 0 {
				scriptsHex += "|"
			}
			scriptsHex += e
		}
	}

	_, err = s.db.Exec(`
		INSERT INTO deals (id, buyer_pubkey, seller_pubkey, escrow_pubkey, escrow_privkey_enc,
			server_pubkey, amount, status, scripts, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		deal.ID,
		pubkeyToHex(deal.BuyerPubKey),
		pubkeyToHex(deal.SellerPubKey),
		pubkeyToHex(deal.EscrowPubKey),
		encPriv,
		pubkeyToHex(deal.ServerPubKey),
		deal.Amount,
		string(deal.Status),
		scriptsHex,
		deal.CreatedAt,
		deal.ExpiresAt,
	)
	return err
}

func (s *SQLiteStore) GetDeal(id string) (*escrow.Deal, error) {
	row := s.db.QueryRow(`
		SELECT id, buyer_pubkey, seller_pubkey, escrow_pubkey, escrow_privkey_enc,
			server_pubkey, amount, status, vtxo_txid, vtxo_vout, scripts,
			created_at, expires_at
		FROM deals WHERE id = ?`, id)

	var (
		dealID, buyerHex, sellerHex, escrowHex, serverHex string
		encPriv                                           []byte
		amount                                            uint64
		status, scriptsHex                                string
		vtxoTxid                                          sql.NullString
		vtxoVout                                          sql.NullInt32
		createdAt, expiresAt                              time.Time
	)

	err := row.Scan(&dealID, &buyerHex, &sellerHex, &escrowHex, &encPriv,
		&serverHex, &amount, &status, &vtxoTxid, &vtxoVout, &scriptsHex,
		&createdAt, &expiresAt)
	if err != nil {
		return nil, fmt.Errorf("deal not found: %w", err)
	}

	buyerKey, err := hexToPubkey(buyerHex)
	if err != nil {
		return nil, err
	}
	sellerKey, err := hexToPubkey(sellerHex)
	if err != nil {
		return nil, err
	}
	escrowPub, err := hexToPubkey(escrowHex)
	if err != nil {
		return nil, err
	}
	serverKey, err := hexToPubkey(serverHex)
	if err != nil {
		return nil, err
	}
	escrowPriv, err := escrow.DecryptPrivateKey(encPriv, s.masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt key: %w", err)
	}

	deal := &escrow.Deal{
		ID:           dealID,
		BuyerPubKey:  buyerKey,
		SellerPubKey: sellerKey,
		EscrowPubKey: escrowPub,
		EscrowPriv:   escrowPriv,
		ServerPubKey: serverKey,
		Amount:       amount,
		Status:       escrow.DealStatus(status),
		CreatedAt:    createdAt,
		ExpiresAt:    expiresAt,
	}

	if vtxoTxid.Valid {
		hash, _ := chainhash.NewHashFromStr(vtxoTxid.String)
		deal.VtxoOutpoint = wire.NewOutPoint(hash, uint32(vtxoVout.Int32))
	}

	if scriptsHex != "" {
		scripts := splitScripts(scriptsHex)
		deal.Script = &script.TapscriptsVtxoScript{}
		if err := deal.Script.Decode(scripts); err != nil {
			return nil, fmt.Errorf("failed to decode scripts: %w", err)
		}
	}

	return deal, nil
}

func (s *SQLiteStore) UpdateStatus(id string, status escrow.DealStatus) error {
	res, err := s.db.Exec("UPDATE deals SET status = ? WHERE id = ?", string(status), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("deal %s not found", id)
	}
	return nil
}

func (s *SQLiteStore) SetVtxoOutpoint(id string, txid string, vout uint32) error {
	_, err := s.db.Exec("UPDATE deals SET vtxo_txid = ?, vtxo_vout = ?, status = ? WHERE id = ?",
		txid, vout, string(escrow.DealFunded), id)
	return err
}

func (s *SQLiteStore) ListDeals(status escrow.DealStatus) ([]*escrow.Deal, error) {
	rows, err := s.db.Query("SELECT id FROM deals WHERE status = ? ORDER BY created_at DESC", string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deals []*escrow.Deal
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		deal, err := s.GetDeal(id)
		if err != nil {
			return nil, err
		}
		deals = append(deals, deal)
	}
	return deals, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func pubkeyToHex(key *btcec.PublicKey) string {
	return hex.EncodeToString(key.SerializeCompressed())
}

func hexToPubkey(h string) (*btcec.PublicKey, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	key, err := btcec.ParsePubKey(b)
	if err != nil {
		return nil, fmt.Errorf("invalid pubkey: %w", err)
	}
	return key, nil
}

func splitScripts(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == '|' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
