package webhooks

import "testing"

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("a-secret-of-arbitrary-length-bytes")
	body := []byte(`{"event":"onramp.completed","amount":"50.00"}`)

	sig := Sign(secret, body)
	if len(sig) != 64 {
		t.Errorf("sig should be 64 hex chars (sha256), got %d", len(sig))
	}
	if !Verify(secret, body, sig) {
		t.Errorf("Verify rejected its own signature")
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	secret := []byte("secret")
	body := []byte(`{"x":1}`)
	sig := Sign(secret, body)

	// Mutate one byte.
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-1] = '2'
	if Verify(secret, tampered, sig) {
		t.Errorf("Verify accepted tampered body")
	}
}

func TestVerifyDetectsWrongSecret(t *testing.T) {
	body := []byte(`{"x":1}`)
	sig := Sign([]byte("right"), body)
	if Verify([]byte("wrong"), body, sig) {
		t.Errorf("Verify accepted wrong secret")
	}
}

func TestVerifyBadHex(t *testing.T) {
	if Verify([]byte("s"), []byte("b"), "not-hex") {
		t.Errorf("Verify accepted malformed signature")
	}
}
