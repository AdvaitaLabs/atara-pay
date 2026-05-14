package tempo

import (
	"crypto/ecdsa"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Keystore holds the private keys for ATARA-Pay-issued Tempo wallets.
//
// The MVP implementation keeps keys in process memory and is intentionally
// simple — production deployments should swap this for a Vault/KMS-backed
// implementation. The Keystore is concurrency-safe.
//
// Address (lowercase hex with 0x prefix) is the lookup key everywhere in the
// adapter, matching what the user receives in API responses.
type Keystore interface {
	Generate() (address string, err error)
	Get(address string) (*ecdsa.PrivateKey, error)
	Has(address string) bool
}

// MemoryKeystore is a process-local Keystore. Keys are lost on restart.
type MemoryKeystore struct {
	mu   sync.RWMutex
	keys map[string]*ecdsa.PrivateKey
}

func NewMemoryKeystore() *MemoryKeystore {
	return &MemoryKeystore{keys: map[string]*ecdsa.PrivateKey{}}
}

// Generate creates a new secp256k1 keypair and returns the lowercase
// 0x-prefixed EVM address.
func (k *MemoryKeystore) Generate() (string, error) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey).Hex()

	k.mu.Lock()
	k.keys[normalize(addr)] = priv
	k.mu.Unlock()

	return addr, nil
}

func (k *MemoryKeystore) Get(address string) (*ecdsa.PrivateKey, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	priv, ok := k.keys[normalize(address)]
	if !ok {
		return nil, fmt.Errorf("tempo: no key for address %s", address)
	}
	return priv, nil
}

func (k *MemoryKeystore) Has(address string) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	_, ok := k.keys[normalize(address)]
	return ok
}

func normalize(addr string) string {
	return common.HexToAddress(addr).Hex()
}
