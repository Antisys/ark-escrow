package escrow

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
)

func TestGenerateEscrowKeyPair(t *testing.T) {
	priv, pub, err := GenerateEscrowKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if priv == nil || pub == nil {
		t.Fatal("keys must not be nil")
	}
	// Public key must match private key
	if !pub.IsEqual(priv.PubKey()) {
		t.Fatal("public key does not match private key")
	}
}

func TestGenerateEscrowKeyPairUniqueness(t *testing.T) {
	priv1, _, _ := GenerateEscrowKeyPair()
	priv2, _, _ := GenerateEscrowKeyPair()
	if bytes.Equal(priv1.Serialize(), priv2.Serialize()) {
		t.Fatal("two generated keys should differ")
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	masterKey := make([]byte, 32)
	rand.Read(masterKey)

	original, _ := btcec.NewPrivateKey()

	encrypted, err := EncryptPrivateKey(original, masterKey)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := DecryptPrivateKey(encrypted, masterKey)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(original.Serialize(), decrypted.Serialize()) {
		t.Fatal("decrypted key does not match original")
	}
}

func TestEncryptDecryptWrongKey(t *testing.T) {
	masterKey1 := make([]byte, 32)
	masterKey2 := make([]byte, 32)
	rand.Read(masterKey1)
	rand.Read(masterKey2)

	original, _ := btcec.NewPrivateKey()

	encrypted, err := EncryptPrivateKey(original, masterKey1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = DecryptPrivateKey(encrypted, masterKey2)
	if err == nil {
		t.Fatal("decryption with wrong key should fail")
	}
}

func TestEncryptInvalidMasterKeyLength(t *testing.T) {
	key, _ := btcec.NewPrivateKey()

	_, err := EncryptPrivateKey(key, []byte("too-short"))
	if err == nil {
		t.Fatal("should reject non-32-byte master key")
	}

	_, err = EncryptPrivateKey(key, make([]byte, 64))
	if err == nil {
		t.Fatal("should reject 64-byte master key")
	}
}

func TestDecryptInvalidMasterKeyLength(t *testing.T) {
	_, err := DecryptPrivateKey([]byte("data"), []byte("short"))
	if err == nil {
		t.Fatal("should reject non-32-byte master key")
	}
}

func TestDecryptTruncatedCiphertext(t *testing.T) {
	masterKey := make([]byte, 32)
	rand.Read(masterKey)

	_, err := DecryptPrivateKey([]byte{0x01, 0x02}, masterKey)
	if err == nil {
		t.Fatal("should reject truncated ciphertext")
	}
}

func TestEncryptedOutputDiffers(t *testing.T) {
	masterKey := make([]byte, 32)
	rand.Read(masterKey)

	key, _ := btcec.NewPrivateKey()

	enc1, _ := EncryptPrivateKey(key, masterKey)
	enc2, _ := EncryptPrivateKey(key, masterKey)

	// Random nonce means same key encrypts to different ciphertext
	if bytes.Equal(enc1, enc2) {
		t.Fatal("same key should produce different ciphertexts (random nonce)")
	}
}
