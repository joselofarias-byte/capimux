package db

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// The existing tamper case replaces a character with one outside base64, so it
// is rejected by the decoder before AES-GCM runs. This test flips one bit in
// every byte of a validly encoded envelope (nonce, ciphertext and tag), so only
// GCM authentication can reject it, and checks the error is the
// authentication one. It also rejects unknown envelope versions and
// truncated blobs. In-memory test keys only.
func TestGROK004DBFieldBitFlipFailsAuthentication(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	const table, row, col = "providerConnections", "account-01", "data"
	plaintext := []byte("synthetic-refresh-token-canary")
	enc, err := encryptDBField(key, table, row, col, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(enc, encryptedDBFieldPrefix))
	if err != nil {
		t.Fatal(err)
	}
	for i := range blob {
		mutated := bytes.Clone(blob)
		mutated[i] ^= 0x01
		encoded := encryptedDBFieldPrefix + base64.RawStdEncoding.EncodeToString(mutated)
		got, err := decryptDBField(key, table, row, col, encoded)
		if err == nil {
			t.Fatalf("byte %d: bit flip accepted, got %q", i, got)
		}
		if !strings.Contains(err.Error(), "authentication failed") {
			t.Fatalf("byte %d: err = %v, want authentication failure", i, err)
		}
	}

	// Truncation below nonce+tag is rejected before Open.
	short := encryptedDBFieldPrefix + base64.RawStdEncoding.EncodeToString(blob[:20])
	if _, err := decryptDBField(key, table, row, col, short); err == nil {
		t.Fatal("accepted truncated ciphertext")
	}
	// Dropping the tag's last byte fails authentication.
	cut := encryptedDBFieldPrefix + base64.RawStdEncoding.EncodeToString(blob[:len(blob)-1])
	if _, err := decryptDBField(key, table, row, col, cut); err == nil {
		t.Fatal("accepted ciphertext with truncated tag")
	}
	// An unknown envelope version must not be parsed as v1.
	v2 := strings.Replace(enc, "capimux:enc:v1:", "capimux:enc:v2:", 1)
	if _, err := decryptDBField(key, table, row, col, v2); err == nil {
		t.Fatal("accepted unknown envelope version")
	}
	// A rotated (different) master key cannot open old ciphertext, and the
	// failure leaves the stored value untouched: decryption is read-only.
	rotated := make([]byte, 32)
	if _, err := rand.Read(rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := decryptDBField(rotated, table, row, col, enc); err == nil {
		t.Fatal("rotated key opened ciphertext sealed under the old key")
	}
	if got, err := decryptDBField(key, table, row, col, enc); err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("original key no longer opens ciphertext after failed attempts: %v", err)
	}
}

// A large sample of encryptions under one key must never repeat a nonce.
func TestGROK004DBFieldNonceUniqueness(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{}, 2000)
	for i := 0; i < 2000; i++ {
		enc, err := encryptDBField(key, "apiKeys", "k", "key", []byte("same-value"))
		if err != nil {
			t.Fatal(err)
		}
		blob, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(enc, encryptedDBFieldPrefix))
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(blob[:12])
		if _, dup := seen[nonce]; dup {
			t.Fatalf("nonce repeated after %d encryptions", i)
		}
		seen[nonce] = struct{}{}
	}
}
