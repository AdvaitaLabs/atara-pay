package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// API key format
// ──────────────────────────────────────────────────────────────────────
//
//	sk_{env}_{base64url-of-24-random-bytes}
//
// Examples:
//
//	sk_test_aB3dE7fGhJ2kL4mN5pQ8rS9tU0vW1xY2-_zA   (env=test)
//	sk_live_…                                       (env=live)
//
// The "secret" payload is 24 random bytes = 192 bits of entropy, encoded as
// base64-url-no-padding for compactness (32 chars). With the "sk_test_" or
// "sk_live_" prefix the full key is exactly 40 chars.
//
// We NEVER store the raw key. The DB column key_hash holds a SHA-256 of the
// full string. key_prefix holds the first KeyPrefixLength chars for
// human-recognizable display ("sk_test_aB3d…").

const (
	// Random payload in raw bytes (before base64).
	apiKeyRandomBytes = 24

	// Visible portion stored in DB (e.g. "sk_test_aB3dE7fGhJ2k" — 8 + 12).
	KeyPrefixLength = 20

	envTest = "test"
	envLive = "live"
)

// ErrMalformedAPIKey is returned by Parse for any string that does not match
// the sk_{env}_{payload} shape.
var ErrMalformedAPIKey = errors.New("auth: malformed API key")

// MintAPIKey generates a fresh API key for the given environment.
//
// Returns:
//
//	raw     - the full secret to hand back to the user exactly once
//	prefix  - the leading KeyPrefixLength chars, safe to store and display
//	hash    - SHA-256 of raw, what goes into api_keys.key_hash
func MintAPIKey(env string) (raw, prefix string, hash []byte, err error) {
	if env != envTest && env != envLive {
		return "", "", nil, fmt.Errorf("auth: invalid env %q (must be 'test' or 'live')", env)
	}

	buf := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", nil, fmt.Errorf("auth: read random: %w", err)
	}

	raw = fmt.Sprintf("sk_%s_%s", env, base64.RawURLEncoding.EncodeToString(buf))
	if len(raw) < KeyPrefixLength {
		// Defensive — should never happen with current sizes.
		return "", "", nil, errors.New("auth: minted key shorter than prefix")
	}
	prefix = raw[:KeyPrefixLength]
	hash = HashAPIKey(raw)
	return raw, prefix, hash, nil
}

// HashAPIKey returns the SHA-256 of the raw key. Used both at creation time
// (to compute key_hash before INSERT) and at request time (to look up the
// key in the api_keys table by hash).
func HashAPIKey(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// ParseAPIKey validates an inbound Authorization header value (the part after
// "Bearer "). On success it returns the environment ("test" or "live") so the
// caller can reject test keys in production and vice versa before even
// hitting the DB.
//
// Constant-time prefix comparison is overkill given the prefix is public,
// but cheap.
func ParseAPIKey(raw string) (env string, err error) {
	if !strings.HasPrefix(raw, "sk_") {
		return "", ErrMalformedAPIKey
	}
	// sk_test_xxx or sk_live_xxx
	switch {
	case subtle.ConstantTimeCompare([]byte(raw[:8]), []byte("sk_test_")) == 1:
		env = envTest
	case subtle.ConstantTimeCompare([]byte(raw[:8]), []byte("sk_live_")) == 1:
		env = envLive
	default:
		return "", ErrMalformedAPIKey
	}
	if len(raw) < KeyPrefixLength {
		return "", ErrMalformedAPIKey
	}
	return env, nil
}
