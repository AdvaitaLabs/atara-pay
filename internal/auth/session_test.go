package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testKey = []byte("a-very-long-test-signing-key-32+")

func TestSessionRoundTrip(t *testing.T) {
	tok, err := NewSessionToken(testKey, "u_x", "tn_y", "owner", time.Hour)
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	if !strings.HasPrefix(tok, "eyJ") {
		t.Errorf("tok doesn't look like a JWT: %q", tok)
	}
	claims, err := ParseSessionToken(testKey, tok)
	if err != nil {
		t.Fatalf("ParseSessionToken: %v", err)
	}
	if claims.UserID != "u_x" || claims.TenantID != "tn_y" || claims.Role != "owner" {
		t.Errorf("claims mismatch: %+v", claims)
	}
}

func TestSessionRejectsBadSignature(t *testing.T) {
	tok, _ := NewSessionToken(testKey, "u_x", "tn_y", "owner", time.Hour)
	other := []byte("a-different-key-also-32-bytes-len")
	if _, err := ParseSessionToken(other, tok); err == nil {
		t.Errorf("expected error with wrong key")
	}
}

func TestSessionRejectsExpired(t *testing.T) {
	// Hand-craft a token with an `exp` claim deep in the past so we exercise
	// the expiration check explicitly. (Using -1h via NewSessionToken's ttl
	// works locally but jwt/v5's leeway varies, so a forged-old exp is the
	// stable signal.)
	claims := SessionClaims{
		UserID:   "u_x",
		TenantID: "tn_y",
		Role:     "owner",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    sessionIssuer,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-48 * time.Hour)),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-48 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-24 * time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(testKey)
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}
	if _, err := ParseSessionToken(testKey, tok); err == nil {
		t.Errorf("expected error for expired token")
	}
}

func TestShortSigningKeyRejected(t *testing.T) {
	if _, err := NewSessionToken([]byte("short"), "u", "t", "owner", time.Hour); err == nil {
		t.Errorf("expected error for short signing key")
	}
}
