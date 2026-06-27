package smtpapi

import (
	"bytes"
	"testing"
)

func testKey() []byte {
	return bytes.Repeat([]byte("k"), 32)
}

func TestEncryptDecryptSecret_RoundTrip(t *testing.T) {
	key := testKey()
	const plaintext = "tanstaaftanstaaf"

	nonceB64, cipherB64, err := EncryptSecret(key, plaintext)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	got, err := DecryptSecret(key, nonceB64, cipherB64)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if got != plaintext {
		t.Fatalf("DecryptSecret = %q, want %q", got, plaintext)
	}
}

func TestEncryptSecret_RandomNonce(t *testing.T) {
	key := testKey()
	nonce1, cipher1, err := EncryptSecret(key, "secret")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	nonce2, cipher2, err := EncryptSecret(key, "secret")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if nonce1 == nonce2 || cipher1 == cipher2 {
		t.Fatal("expected distinct nonce/ciphertext across encryptions of the same plaintext")
	}
}

func TestDecryptSecret_WrongKeyFails(t *testing.T) {
	nonceB64, cipherB64, err := EncryptSecret(testKey(), "secret")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	wrongKey := bytes.Repeat([]byte("x"), 32)
	if _, err := DecryptSecret(wrongKey, nonceB64, cipherB64); err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDecryptSecret_TamperedCiphertextFails(t *testing.T) {
	key := testKey()
	nonceB64, cipherB64, err := EncryptSecret(key, "secret")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	tampered := "A" + cipherB64[1:]
	if _, err := DecryptSecret(key, nonceB64, tampered); err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}
