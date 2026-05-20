package tempo

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
)

// PrivateKeyBytes is the size of a serialized secp256k1 private key.
// (32 bytes; go-ethereum stores them as a fixed-length big-endian integer.)
const PrivateKeyBytes = 32

// GenerateKeypair mints a fresh secp256k1 keypair, returning the private-key
// bytes and the derived 0x-prefixed EVM address.
//
// This is the primitive the orchestrator layer wraps in transactions:
//
//	priv, addr, _ := tempo.GenerateKeypair()
//	blob, ver, _ := ks.Encrypt(priv)
//	// INSERT wallets (address, encrypted_private_key=blob, key_version=ver)
//
// The Tempo Adapter never sees the unencrypted bytes after this call.
func GenerateKeypair() (priv [PrivateKeyBytes]byte, address string, err error) {
	k, kerr := crypto.GenerateKey()
	if kerr != nil {
		return priv, "", fmt.Errorf("tempo: generate key: %w", kerr)
	}
	copy(priv[:], crypto.FromECDSA(k))
	address = crypto.PubkeyToAddress(k.PublicKey).Hex()
	return priv, address, nil
}

// AddressFromPrivateKey derives the EVM address that signs with priv. Used
// by the orchestrator on every sign path to sanity-check that the decrypted
// blob matches the wallet row's address.
func AddressFromPrivateKey(priv []byte) (string, error) {
	k, err := privateKeyFromBytes(priv)
	if err != nil {
		return "", err
	}
	return crypto.PubkeyToAddress(k.PublicKey).Hex(), nil
}

func privateKeyFromBytes(b []byte) (*ecdsa.PrivateKey, error) {
	if len(b) != PrivateKeyBytes {
		return nil, fmt.Errorf("tempo: private key must be %d bytes (got %d)",
			PrivateKeyBytes, len(b))
	}
	k, err := crypto.ToECDSA(b)
	if err != nil {
		return nil, fmt.Errorf("tempo: decode private key: %w", err)
	}
	return k, nil
}

