// Package auth holds ATARA-Pay's authentication primitives:
// password hashing, API-key minting and lookup, and the HTTP middleware
// that wires Bearer tokens to a tenant context.
//
// password.go covers password hashing using argon2id, the Argon2 winner of
// the Password Hashing Competition and OWASP's current recommendation.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. These follow the OWASP 2024 recommendation for
// interactive logins; raise memory + iterations as hardware improves.
const (
	a2Memory      = 64 * 1024 // 64 MiB
	a2Iterations  = 3
	a2Parallelism = 2
	a2SaltLength  = 16
	a2KeyLength   = 32
)

// HashPassword turns a plaintext password into a PHC-format argon2id string:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<saltB64>$<hashB64>
//
// The returned value goes directly into users.password_hash.
func HashPassword(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("auth: password is empty")
	}

	salt := make([]byte, a2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(plaintext), salt,
		a2Iterations, a2Memory, a2Parallelism, a2KeyLength,
	)

	enc := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, a2Memory, a2Iterations, a2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return enc, nil
}

// VerifyPassword returns nil iff plaintext hashes to the same digest as
// encoded. Comparison is constant-time so timing attacks can't probe for
// near-misses.
func VerifyPassword(encoded, plaintext string) error {
	salt, expectedHash, params, err := parseArgon2(encoded)
	if err != nil {
		return err
	}

	actualHash := argon2.IDKey(
		[]byte(plaintext), salt,
		params.iterations, params.memory, params.parallelism,
		uint32(len(expectedHash)),
	)

	if subtle.ConstantTimeCompare(expectedHash, actualHash) != 1 {
		return errors.New("auth: password mismatch")
	}
	return nil
}

type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parseArgon2(encoded string) (salt, hash []byte, params argon2Params, err error) {
	parts := strings.Split(encoded, "$")
	// Expected:  ["", "argon2id", "v=19", "m=…,t=…,p=…", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, params, errors.New("auth: not an argon2id hash")
	}

	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, params, fmt.Errorf("auth: parse version: %w", err)
	}
	if version != argon2.Version {
		return nil, nil, params, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}

	if _, err = fmt.Sscanf(
		parts[3], "m=%d,t=%d,p=%d",
		&params.memory, &params.iterations, &params.parallelism,
	); err != nil {
		return nil, nil, params, fmt.Errorf("auth: parse params: %w", err)
	}

	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return nil, nil, params, fmt.Errorf("auth: decode salt: %w", err)
	}
	if hash, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return nil, nil, params, fmt.Errorf("auth: decode hash: %w", err)
	}
	return salt, hash, params, nil
}
