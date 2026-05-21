package auth

import (
	"strings"
	"testing"
)

func TestMintAPIKeyShape(t *testing.T) {
	raw, prefix, hash, err := MintAPIKey("test")
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	if !strings.HasPrefix(raw, "sk_test_") {
		t.Errorf("raw should start with sk_test_, got %q", raw)
	}
	if len(prefix) != KeyPrefixLength || !strings.HasPrefix(raw, prefix) {
		t.Errorf("prefix mismatch: %q vs raw %q", prefix, raw)
	}
	if len(hash) != 32 {
		t.Errorf("hash should be 32 bytes (sha256), got %d", len(hash))
	}
}

func TestMintAPIKeyBadEnv(t *testing.T) {
	if _, _, _, err := MintAPIKey("staging"); err == nil {
		t.Errorf("expected error for non-test/live env")
	}
}

func TestHashAPIKeyDeterministic(t *testing.T) {
	raw := "sk_test_abc"
	if string(HashAPIKey(raw)) != string(HashAPIKey(raw)) {
		t.Errorf("hash must be deterministic")
	}
}

func TestParseAPIKey(t *testing.T) {
	cases := []struct {
		key     string
		wantEnv string
		wantErr bool
	}{
		{"sk_test_abcdefghijkl", "test", false},
		{"sk_live_abcdefghijkl", "live", false},
		{"sk_prod_abcdefghijkl", "", true},
		{"pk_test_abcdefghijkl", "", true},
		{"sk_test_", "", true}, // too short
		{"", "", true},
	}
	for _, c := range cases {
		env, err := ParseAPIKey(c.key)
		if c.wantErr && err == nil {
			t.Errorf("ParseAPIKey(%q): expected error", c.key)
			continue
		}
		if !c.wantErr && err != nil {
			t.Errorf("ParseAPIKey(%q): unexpected error %v", c.key, err)
			continue
		}
		if env != c.wantEnv {
			t.Errorf("ParseAPIKey(%q): env = %q, want %q", c.key, env, c.wantEnv)
		}
	}
}

func TestMintedKeyParses(t *testing.T) {
	// Round-trip: every minted key must parse back to its env.
	for _, env := range []string{"test", "live"} {
		raw, _, _, _ := MintAPIKey(env)
		got, err := ParseAPIKey(raw)
		if err != nil || got != env {
			t.Errorf("mint→parse round trip failed for env=%s: got env=%q err=%v",
				env, got, err)
		}
	}
}
