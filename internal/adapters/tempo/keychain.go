package tempo

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// AccountKeychain precompile at the deterministic address Tempo enshrines.
// Reference: TIP-1053. This is where authorizeKey / revokeKey live; calling
// it with the wallet's master key registers a session key on-chain with
// hard spending limits enforced by the precompile itself.
const AccountKeychainAddress = "0xAAAAAAAA00000000000000000000000000000000"

// SignatureType selects how the keychain precompile will verify signatures
// produced BY the session key. We only use secp256k1 today — same scheme
// our adapter already signs with.
type SignatureType uint8

const (
	SigTypeSecp256k1 SignatureType = 0
	SigTypeP256      SignatureType = 1
	SigTypeWebAuthn  SignatureType = 2
)

// TokenLimit caps how much of `Token` the session key may move per
// `Period` (seconds). One TokenLimit per (token, period) pair — multiple
// entries can share the same token to express daily+weekly+monthly.
//
// Amount is in the token's native precision (TIP-20 pathUSD is 6 decimals,
// so $20 = 20_000_000).
type TokenLimit struct {
	Token  common.Address `abi:"token"`
	Amount *big.Int       `abi:"amount"`
	Period uint64         `abi:"period"`
}

// AllowedCall scopes the session key to a specific (contract, function
// selector) pair. Empty list + AllowAnyCalls=false means the key can't
// invoke anything; the precompile's transfer routing always works
// regardless.
type AllowedCall struct {
	Target   common.Address `abi:"target"`
	Selector [4]byte        `abi:"selector"`
}

// KeyRestrictions is the full authorization envelope passed to
// authorizeKey. The precompile stores it on-chain and enforces it on
// every subsequent signature from the session key.
type KeyRestrictions struct {
	Expiry        uint64        `abi:"expiry"`
	EnforceLimits bool          `abi:"enforceLimits"`
	Limits        []TokenLimit  `abi:"limits"`
	AllowAnyCalls bool          `abi:"allowAnyCalls"`
	AllowedCalls  []AllowedCall `abi:"allowedCalls"`
}

// ──────────────────────────────────────────────────────────────────────
// ABI plumbing
// ──────────────────────────────────────────────────────────────────────

// keychainABI is the single function we care about: authorizeKey. We
// declare the full tuple shape — go-ethereum's abi.JSON parses the nested
// components into the right schema.
const keychainABI = `[{
  "name": "authorizeKey",
  "type": "function",
  "stateMutability": "nonpayable",
  "inputs": [
    {"name": "keyId", "type": "address"},
    {"name": "signatureType", "type": "uint8"},
    {
      "name": "config",
      "type": "tuple",
      "components": [
        {"name": "expiry", "type": "uint64"},
        {"name": "enforceLimits", "type": "bool"},
        {
          "name": "limits", "type": "tuple[]",
          "components": [
            {"name": "token", "type": "address"},
            {"name": "amount", "type": "uint256"},
            {"name": "period", "type": "uint64"}
          ]
        },
        {"name": "allowAnyCalls", "type": "bool"},
        {
          "name": "allowedCalls", "type": "tuple[]",
          "components": [
            {"name": "target", "type": "address"},
            {"name": "selector", "type": "bytes4"}
          ]
        }
      ]
    }
  ],
  "outputs": []
}]`

var parsedKeychain abi.ABI

func init() {
	a, err := abi.JSON(strings.NewReader(keychainABI))
	if err != nil {
		panic(fmt.Errorf("tempo: parse keychain abi: %w", err))
	}
	parsedKeychain = a
}

// PackAuthorizeKey returns the calldata for an authorizeKey() invocation.
// Pass it as the Data field of an EVM tx whose To = AccountKeychainAddress.
//
// keyId is the EVM address of the session key being authorized.
func PackAuthorizeKey(keyId common.Address, sigType SignatureType, r KeyRestrictions) ([]byte, error) {
	if r.Expiry == 0 && r.EnforceLimits {
		// A 0 expiry with EnforceLimits=true is almost certainly a bug —
		// the chain treats it as "never expires" so the session key would
		// outlive the gateway intention.
		return nil, errors.New("tempo: KeyRestrictions.Expiry=0 with EnforceLimits=true; pass an explicit expiry")
	}
	// abi.Pack auto-encodes nested tuples via the struct tags.
	data, err := parsedKeychain.Pack("authorizeKey", keyId, uint8(sigType), r)
	if err != nil {
		return nil, fmt.Errorf("tempo: pack authorizeKey: %w", err)
	}
	return data, nil
}

// ──────────────────────────────────────────────────────────────────────
// Convenience builders
// ──────────────────────────────────────────────────────────────────────

// PathUSDDailyLimit produces a single TokenLimit for pathUSD at one-day
// resolution. The orchestrator uses this for the common case: "agent may
// spend at most N USD of pathUSD per day."
//
// dailyMicros is the limit in microUSD (1e6 scale matches TIP-20 6-decimal
// precision); 20 USD per day = 20_000_000 micros.
func PathUSDDailyLimit(dailyMicros int64) TokenLimit {
	return TokenLimit{
		Token:  common.HexToAddress(PathUSDAddress),
		Amount: big.NewInt(dailyMicros),
		Period: 86400, // seconds in a day
	}
}

// TransferOnly returns an AllowedCall pinning the session key to standard
// ERC-20 transfer(address,uint256) on the supplied token. selector is the
// canonical 0xa9059cbb.
func TransferOnly(token common.Address) AllowedCall {
	return AllowedCall{
		Target:   token,
		Selector: [4]byte{0xa9, 0x05, 0x9c, 0xbb},
	}
}
