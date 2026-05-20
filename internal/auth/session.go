package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Session tokens are HS256-signed JWTs handed out by POST /login. They carry
// the minimum identity needed for the dashboard to call protected endpoints
// without re-reading the database on every hop.
//
// Token shape (claims):
//
//	{
//	  "sub": "u_01J…",         // user id
//	  "tid": "tn_01J…",        // tenant id
//	  "role": "owner",         // user role
//	  "exp": 1735689600,
//	  "iat": …,
//	  "iss": "atara-pay"
//	}
//
// Sessions are stateless. To force-revoke, raise the signing key (rotates
// every active token simultaneously) or layer a per-tenant revocation list
// when that requirement appears.

const (
	sessionIssuer  = "atara-pay"
	sessionDefault = 24 * time.Hour
)

// ErrInvalidSession is returned by ParseSessionToken for any token we can't
// verify (bad signature, expired, wrong issuer, etc.).
var ErrInvalidSession = errors.New("auth: invalid session token")

// SessionClaims is the typed view of what NewSessionToken puts into a token
// and what ParseSessionToken returns.
type SessionClaims struct {
	UserID   string `json:"sub"`
	TenantID string `json:"tid"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// NewSessionToken signs and returns a JWT for the given user. ttl=0 falls
// back to the 24-hour default.
func NewSessionToken(signingKey []byte, userID, tenantID, role string, ttl time.Duration) (string, error) {
	if len(signingKey) < 32 {
		return "", errors.New("auth: signing key must be at least 32 bytes")
	}
	if ttl <= 0 {
		ttl = sessionDefault
	}
	now := time.Now().UTC()
	claims := SessionClaims{
		UserID:   userID,
		TenantID: tenantID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    sessionIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("auth: sign session: %w", err)
	}
	return signed, nil
}

// ParseSessionToken verifies a token's signature, expiry, and issuer.
// Returns the typed claims on success.
func ParseSessionToken(signingKey []byte, raw string) (*SessionClaims, error) {
	claims := &SessionClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return signingKey, nil
	}, jwt.WithIssuer(sessionIssuer), jwt.WithExpirationRequired())
	if err != nil || !tok.Valid {
		return nil, ErrInvalidSession
	}
	return claims, nil
}
