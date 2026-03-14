package escrow

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/btcsuite/btcd/btcec/v2"
)

// GenerateEscrowKeyPair creates a fresh secp256k1 keypair for a single deal.
// Each deal gets its own keypair to isolate risk.
func GenerateEscrowKeyPair() (*btcec.PrivateKey, *btcec.PublicKey, error) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate keypair: %w", err)
	}
	return privKey, privKey.PubKey(), nil
}

// EncryptPrivateKey encrypts a private key using AES-256-GCM with a master key.
func EncryptPrivateKey(privKey *btcec.PrivateKey, masterKey []byte) ([]byte, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes (AES-256)")
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	plaintext := privKey.Serialize()
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// DecryptPrivateKey decrypts an AES-256-GCM encrypted private key.
func DecryptPrivateKey(ciphertext, masterKey []byte) (*btcec.PrivateKey, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes (AES-256)")
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, encrypted := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	privKey, _ := btcec.PrivKeyFromBytes(plaintext)
	return privKey, nil
}
