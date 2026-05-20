// Package keystore encrypts and decrypts secrets at rest.
//
// Secrets (currently Tempo private keys, later session-key blobs) are
// AES-256-GCM encrypted with a master key loaded from env at boot. The
// keystore supports multiple concurrent master-key versions so we can
// rotate without downtime:
//
//	v=1 was generated 2026-04-01; ciphertext stored under key_version=1
//	v=2 generated 2026-06-01; new writes go to v=2, v=1 still decrypts on read
//	once every row is re-encrypted to v=2, v=1 can be removed from config
//
// The wallets table stores (encrypted_private_key BYTEA, key_version
// SMALLINT). The keystore reads/writes both together so we never try to
// decrypt with the wrong generation.
package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// MasterKeyBytes is the required length of each master key.
const MasterKeyBytes = 32

// ErrUnknownVersion is returned by Decrypt when the ciphertext's version is
// not configured in the keystore.
var ErrUnknownVersion = errors.New("keystore: unknown key version")

// Keystore is the application-wide interface. Implementations must be
// concurrency-safe; the default AESKeystore is.
type Keystore interface {
	// Encrypt seals plaintext under the current master-key version. Returns
	// the ciphertext and the version it was sealed with (for storage).
	Encrypt(plaintext []byte) (ciphertext []byte, version int16, err error)

	// Decrypt opens a ciphertext previously sealed by Encrypt with the given
	// version. Returns ErrUnknownVersion if version is not in this keystore.
	Decrypt(ciphertext []byte, version int16) (plaintext []byte, err error)

	// CurrentVersion is the version Encrypt will tag new writes with. Used
	// by rotation jobs to know which existing rows still need re-encryption.
	CurrentVersion() int16
}

// AESKeystore is the default Keystore. It holds N master keys keyed by
// version. Writes use `current`; reads pick the right key from the version
// passed in.
type AESKeystore struct {
	keys    map[int16]cipher.AEAD
	current int16
}

// NewAESKeystore builds an AESKeystore from a version → 32-byte key map.
// Returns an error if:
//   - the map is empty
//   - any key is not exactly MasterKeyBytes long
//   - current is not present in the map
func NewAESKeystore(keysByVersion map[int16][]byte, current int16) (*AESKeystore, error) {
	if len(keysByVersion) == 0 {
		return nil, errors.New("keystore: at least one master key is required")
	}

	aeadByVersion := make(map[int16]cipher.AEAD, len(keysByVersion))
	for v, k := range keysByVersion {
		if len(k) != MasterKeyBytes {
			return nil, fmt.Errorf("keystore: v=%d key must be %d bytes (got %d)",
				v, MasterKeyBytes, len(k))
		}
		block, err := aes.NewCipher(k)
		if err != nil {
			return nil, fmt.Errorf("keystore: v=%d new cipher: %w", v, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("keystore: v=%d new GCM: %w", v, err)
		}
		aeadByVersion[v] = aead
	}

	if _, ok := aeadByVersion[current]; !ok {
		return nil, fmt.Errorf("keystore: current version v=%d not in keystore", current)
	}

	return &AESKeystore{keys: aeadByVersion, current: current}, nil
}

// Encrypt seals plaintext as [nonce || ciphertext || tag] and returns the
// current version alongside.
func (k *AESKeystore) Encrypt(plaintext []byte) ([]byte, int16, error) {
	aead := k.keys[k.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, fmt.Errorf("keystore: read nonce: %w", err)
	}
	// Seal appends to nonce so the result is [nonce || ct || tag].
	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return sealed, k.current, nil
}

// Decrypt opens a sealed blob using the AEAD registered under version.
func (k *AESKeystore) Decrypt(blob []byte, version int16) ([]byte, error) {
	aead, ok := k.keys[version]
	if !ok {
		return nil, fmt.Errorf("%w: v=%d", ErrUnknownVersion, version)
	}
	ns := aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("keystore: ciphertext shorter than nonce")
	}
	nonce, ct := blob[:ns], blob[ns:]
	plaintext, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		// Constant message — never leak whether the version was right but the
		// tag failed (rotated key, tampering) vs simply truncated input.
		return nil, errors.New("keystore: decrypt failed")
	}
	return plaintext, nil
}

// CurrentVersion returns the version Encrypt is currently writing.
func (k *AESKeystore) CurrentVersion() int16 { return k.current }
