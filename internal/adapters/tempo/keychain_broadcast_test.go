package tempo

import (
	"context"
	"strings"
	"testing"
)

// The full AuthorizeKey path requires a live Tempo RPC and a funded
// wallet, so end-to-end coverage lives in the integration suite. This
// unit test focuses on the defensive guard that fires BEFORE any
// network traffic — the address↔private-key mismatch check.

func TestAuthorizeKeyRejectsAddressMismatch(t *testing.T) {
	// Build a real keypair so the inner derivation is exact.
	priv, addr, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	// An Adapter with nil client is fine here — the mismatch check fires
	// before any RPC call.
	a := &Adapter{client: nil}

	_, err = a.AuthorizeKey(context.Background(), AuthorizeKeyInput{
		WalletPrivKey: priv[:],
		// Deliberately wrong wallet address (zero address; the keypair
		// above will derive something else).
		WalletAddress:     "0x0000000000000000000000000000000000000000",
		SessionKeyAddress: addr, // arbitrary
		SignatureType:     SigTypeSecp256k1,
		Restrictions: KeyRestrictions{
			Expiry:        1735689600,
			EnforceLimits: true,
		},
	})
	if err == nil {
		t.Fatalf("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "private key derives") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestAuthorizeKeyRejectsBadPrivateKey(t *testing.T) {
	a := &Adapter{client: nil}

	_, err := a.AuthorizeKey(context.Background(), AuthorizeKeyInput{
		WalletPrivKey:     []byte{0x01, 0x02}, // wrong length
		WalletAddress:     "0xabc",
		SessionKeyAddress: "0xdef",
		SignatureType:     SigTypeSecp256k1,
		Restrictions: KeyRestrictions{
			Expiry: 1735689600, EnforceLimits: true,
		},
	})
	if err == nil {
		t.Fatalf("expected error for malformed private key")
	}
}
