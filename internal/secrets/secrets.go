package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// DeriveKey builds a 32-byte AES key from TRADING_MASTER_KEY env (any length).
func DeriveKey() ([]byte, error) {
	raw := os.Getenv("TRADING_MASTER_KEY")
	if raw == "" {
		return nil, errors.New("TRADING_MASTER_KEY not set")
	}
	h := sha256.Sum256([]byte(raw))
	return h[:], nil
}

// EncryptAESGCM encrypts plaintext; returns base64(nonce||ciphertext).
func EncryptAESGCM(plaintext []byte, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// DecryptAESGCM reverses EncryptAESGCM.
func DecryptAESGCM(encoded string, key []byte) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:ns], data[ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

// DecryptString decrypts to UTF-8 string; empty input returns empty.
func DecryptString(encoded string, key []byte) (string, error) {
	if encoded == "" {
		return "", nil
	}
	b, err := DecryptAESGCM(encoded, key)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
