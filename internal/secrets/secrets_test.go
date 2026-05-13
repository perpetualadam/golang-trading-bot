package secrets

import (
	"crypto/sha256"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := sha256.Sum256([]byte("test-master-key-material"))
	plain := "sk_live_abc123"
	enc, err := EncryptString(plain, key[:])
	if err != nil {
		t.Fatal(err)
	}
	if enc == "" || enc == plain {
		t.Fatalf("unexpected ciphertext")
	}
	got, err := DecryptString(enc, key[:])
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("got %q want %q", got, plain)
	}
}

func TestEncryptDecryptEmpty(t *testing.T) {
	key := sha256.Sum256([]byte("k"))
	e, err := EncryptString("", key[:])
	if err != nil || e != "" {
		t.Fatalf("EncryptString empty: %q %v", e, err)
	}
	d, err := DecryptString("", key[:])
	if err != nil || d != "" {
		t.Fatalf("DecryptString empty: %q %v", d, err)
	}
}
