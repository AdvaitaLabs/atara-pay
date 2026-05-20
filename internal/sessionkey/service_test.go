package sessionkey

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
	"github.com/atara-xyz/atara-pay/internal/keystore"
)

// The DB path is integration-tested elsewhere (requires a live Postgres
// connection). These unit tests focus on the pieces that DON'T touch the
// database — input validation, keypair / encryption round-trip, defaults.

func TestValidateMint(t *testing.T) {
	mk := func(mut func(*MintInput)) MintInput {
		in := MintInput{
			TenantID: "tn_x", WalletID: "wlt_a", GroupID: "wg_a",
			Name: "alice-agent",
		}
		if mut != nil {
			mut(&in)
		}
		return in
	}

	ok := func(in MintInput) {
		t.Helper()
		if err := validateMint(in); err != nil {
			t.Errorf("expected ok; got %v", err)
		}
	}
	bad := func(in MintInput, what string) {
		t.Helper()
		if err := validateMint(in); err == nil {
			t.Errorf("expected error (%s) — got nil", what)
		}
	}

	ok(mk(nil))
	bad(mk(func(in *MintInput) { in.TenantID = "" }), "no tenant")
	bad(mk(func(in *MintInput) { in.WalletID = "" }), "no wallet")
	bad(mk(func(in *MintInput) { in.GroupID = "" }), "no group")
	bad(mk(func(in *MintInput) { in.Name = "" }), "no name")
	bad(mk(func(in *MintInput) { in.RotationMode = "weird" }), "bad rotation mode")
	bad(mk(func(in *MintInput) { in.Limits.DailyUSD = -5 }), "negative cap")
	bad(mk(func(in *MintInput) { in.ExpiresAt = time.Now().Add(-time.Hour) }), "past expiry")
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	// Build a keystore with one master key.
	masterKey := make([]byte, keystore.MasterKeyBytes)
	if _, err := rand.Read(masterKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	ks, err := keystore.NewAESKeystore(
		map[int16][]byte{1: masterKey}, 1,
	)
	if err != nil {
		t.Fatalf("NewAESKeystore: %v", err)
	}

	// Simulate what Mint() does inside its transaction, without actually
	// touching the DB. This proves the keystore round-trip and the address
	// re-derivation invariant hold.
	priv, addr, err := tempo.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	plain := append([]byte(nil), priv[:]...)

	enc, ver, err := ks.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := ks.Decrypt(enc, ver)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	// Derived address must match what GenerateKeypair returned.
	derived, err := tempo.AddressFromPrivateKey(got)
	if err != nil {
		t.Fatalf("AddressFromPrivateKey: %v", err)
	}
	if derived != addr {
		t.Errorf("address mismatch: derived=%s mint=%s", derived, addr)
	}
}

func TestMarshalStringList(t *testing.T) {
	if string(marshalStringList(nil)) != "[]" {
		t.Errorf("nil should marshal to [], got %s", marshalStringList(nil))
	}
	if string(marshalStringList([]string{})) != "[]" {
		t.Errorf("empty slice should marshal to []")
	}
	if string(marshalStringList([]string{"a", "b"})) != `["a","b"]` {
		t.Errorf("two-element slice marshal wrong: %s", marshalStringList([]string{"a", "b"}))
	}
}
