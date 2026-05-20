package auth

import "testing"

func TestHashAndVerify(t *testing.T) {
	enc, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword(enc, "correct horse battery staple"); err != nil {
		t.Errorf("VerifyPassword (correct): %v", err)
	}
	if err := VerifyPassword(enc, "wrong"); err == nil {
		t.Errorf("VerifyPassword (wrong): expected error, got nil")
	}
}

func TestHashEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Errorf("HashPassword(empty): expected error")
	}
}

func TestEachHashIsUnique(t *testing.T) {
	// Same plaintext must produce different hashes (salts must be random).
	h1, _ := HashPassword("hello")
	h2, _ := HashPassword("hello")
	if h1 == h2 {
		t.Errorf("two hashes of the same password collided — salt is not random")
	}
}
