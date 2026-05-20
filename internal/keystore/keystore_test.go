package keystore

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, MasterKeyBytes)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	k := mustKey(t)
	ks, err := NewAESKeystore(map[int16][]byte{1: k}, 1)
	if err != nil {
		t.Fatalf("NewAESKeystore: %v", err)
	}
	plaintext := []byte("super-secret-tempo-private-key-bytes")

	ct, v, err := ks.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if v != 1 {
		t.Errorf("version = %d, want 1", v)
	}

	pt, err := ks.Decrypt(ct, v)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("round-trip mismatch")
	}
}

func TestRotationCoexistence(t *testing.T) {
	// Two keys configured, current = v2. Old ciphertext (v1) must still
	// decrypt cleanly while new writes go to v2.
	k1, k2 := mustKey(t), mustKey(t)
	ksV1, _ := NewAESKeystore(map[int16][]byte{1: k1}, 1)
	ctV1, _, _ := ksV1.Encrypt([]byte("old-secret"))

	ks, err := NewAESKeystore(map[int16][]byte{1: k1, 2: k2}, 2)
	if err != nil {
		t.Fatalf("NewAESKeystore: %v", err)
	}
	if ks.CurrentVersion() != 2 {
		t.Errorf("current = %d, want 2", ks.CurrentVersion())
	}

	// v1 ciphertext still readable.
	if _, err := ks.Decrypt(ctV1, 1); err != nil {
		t.Errorf("old ciphertext should still decrypt: %v", err)
	}

	// New writes get v2.
	_, v, _ := ks.Encrypt([]byte("new-secret"))
	if v != 2 {
		t.Errorf("new write went to v=%d, want 2", v)
	}
}

func TestUnknownVersion(t *testing.T) {
	k := mustKey(t)
	ks, _ := NewAESKeystore(map[int16][]byte{1: k}, 1)
	ct, _, _ := ks.Encrypt([]byte("x"))

	_, err := ks.Decrypt(ct, 99)
	if !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("expected ErrUnknownVersion, got %v", err)
	}
}

func TestTamperDetected(t *testing.T) {
	k := mustKey(t)
	ks, _ := NewAESKeystore(map[int16][]byte{1: k}, 1)
	ct, _, _ := ks.Encrypt([]byte("hello"))

	// Flip a byte in the ciphertext — GCM tag verification must reject.
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 0xff

	if _, err := ks.Decrypt(tampered, 1); err == nil {
		t.Errorf("expected decrypt failure on tampered ciphertext")
	}
}

func TestConstructorRejectsBadInput(t *testing.T) {
	good := mustKey(t)
	short := make([]byte, 16)

	cases := []struct {
		name string
		keys map[int16][]byte
		cur  int16
	}{
		{"empty map", map[int16][]byte{}, 1},
		{"wrong size", map[int16][]byte{1: short}, 1},
		{"current not in map", map[int16][]byte{1: good}, 7},
	}
	for _, c := range cases {
		if _, err := NewAESKeystore(c.keys, c.cur); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestNonceUniqueness(t *testing.T) {
	// Two encrypts of the same plaintext must produce different ciphertexts
	// (proves the nonce is random, not deterministic).
	k := mustKey(t)
	ks, _ := NewAESKeystore(map[int16][]byte{1: k}, 1)
	ct1, _, _ := ks.Encrypt([]byte("same input"))
	ct2, _, _ := ks.Encrypt([]byte("same input"))
	if bytes.Equal(ct1, ct2) {
		t.Errorf("nonce is deterministic — two identical encrypts collided")
	}
}
