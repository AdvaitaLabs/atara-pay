package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HMAC-SHA256 signing for outbound webhook bodies.
//
// Customers verify by recomputing HMAC-SHA256(secret, raw_body) and
// constant-time-comparing against the X-Atara-Signature header value
// (lowercase hex). The X-Atara-Signature-Version header identifies which
// secret revision we signed with — important during a secret rotation
// window when both the prior and the new secret are temporarily valid.
//
// The signing scheme is exactly the request body bytes (no timestamp
// prefix). Customers who need replay protection should use the
// X-Atara-Event-Id header as the idempotency key on their side — every
// webhook_event has a unique id.

// Sign returns the lowercase-hex HMAC-SHA256 of body under secret.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify is the symmetric helper customers can copy into their own
// codebase. We keep it in the same package so internal tests can use it
// without duplicating logic. It compares in constant time.
func Verify(secret, body []byte, signatureHex string) bool {
	want, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(want, mac.Sum(nil))
}
