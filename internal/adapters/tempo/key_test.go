package tempo

import (
	"strings"
	"testing"
)

func TestGenerateKeypairProducesValidAddress(t *testing.T) {
	priv, addr, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	if !strings.HasPrefix(addr, "0x") || len(addr) != 42 {
		t.Errorf("address = %q, want 42-char 0x-prefixed", addr)
	}
	if priv == ([PrivateKeyBytes]byte{}) {
		t.Errorf("zero private key — entropy sink broken")
	}
}

func TestAddressFromPrivateKeyMatchesGenerate(t *testing.T) {
	priv, addr, _ := GenerateKeypair()
	got, err := AddressFromPrivateKey(priv[:])
	if err != nil {
		t.Fatalf("AddressFromPrivateKey: %v", err)
	}
	// Same EVM checksum representation expected from both paths.
	if got != addr {
		t.Errorf("derived %s, generator produced %s", got, addr)
	}
}

func TestAddressFromPrivateKeyRejectsBadLength(t *testing.T) {
	for _, bad := range [][]byte{nil, {0x01}, make([]byte, 31), make([]byte, 33)} {
		if _, err := AddressFromPrivateKey(bad); err == nil {
			t.Errorf("expected error for %d-byte key", len(bad))
		}
	}
}

func TestEachGenerateIsUnique(t *testing.T) {
	// Two keypairs should never collide.
	_, a1, _ := GenerateKeypair()
	_, a2, _ := GenerateKeypair()
	if a1 == a2 {
		t.Errorf("two GenerateKeypair calls produced the same address — RNG broken")
	}
}
