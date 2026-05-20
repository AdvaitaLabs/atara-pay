package tempo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/crypto/sha3"
)

func TestPackAuthorizeKeySelector(t *testing.T) {
	// The first 4 bytes of any function calldata are keccak256(signature)
	// truncated. We compute the signature manually here and verify the
	// emitted calldata leads with the same selector — guards against
	// accidental ABI definition drift.
	sig := []byte(
		"authorizeKey(address,uint8,(uint64,bool,(address,uint256,uint64)[],bool,(address,bytes4)[]))",
	)
	hash := sha3.NewLegacyKeccak256()
	hash.Write(sig)
	wantSelector := hash.Sum(nil)[:4]

	data, err := PackAuthorizeKey(
		common.HexToAddress("0x0000000000000000000000000000000000000001"),
		SigTypeSecp256k1,
		KeyRestrictions{
			Expiry:        1735689600, // 2025-01-01
			EnforceLimits: true,
			Limits:        []TokenLimit{},
			AllowedCalls:  []AllowedCall{},
		},
	)
	if err != nil {
		t.Fatalf("PackAuthorizeKey: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("calldata too short")
	}
	if !bytes.Equal(data[:4], wantSelector) {
		t.Errorf("selector mismatch:\n  got  %s\n  want %s",
			hex.EncodeToString(data[:4]), hex.EncodeToString(wantSelector))
	}

	// Sanity: subsequent words present (selector + 3 head words at minimum
	// + tuple body). 4 + 32*3 = 100 bytes minimum.
	if len(data) < 100 {
		t.Errorf("calldata length = %d, expected >= 100", len(data))
	}
}

func TestPackAuthorizeKeyDeterministic(t *testing.T) {
	build := func() []byte {
		out, err := PackAuthorizeKey(
			common.HexToAddress("0x00000000000000000000000000000000000000A1"),
			SigTypeSecp256k1,
			KeyRestrictions{
				Expiry:        1735689600,
				EnforceLimits: true,
				Limits: []TokenLimit{
					{
						Token:  common.HexToAddress(PathUSDAddress),
						Amount: big.NewInt(20_000_000),
						Period: 86400,
					},
				},
				AllowedCalls: []AllowedCall{
					TransferOnly(common.HexToAddress(PathUSDAddress)),
				},
			},
		)
		if err != nil {
			t.Fatalf("PackAuthorizeKey: %v", err)
		}
		return out
	}

	a := build()
	b := build()
	if !bytes.Equal(a, b) {
		t.Errorf("same input produced different calldata — encoder is non-deterministic")
	}

	// Hash it so failing diffs in CI show a stable signature.
	h := sha256.Sum256(a)
	t.Logf("calldata sha256 = %s", hex.EncodeToString(h[:]))
}

func TestPackAuthorizeKeyRejectsZeroExpiryWithLimits(t *testing.T) {
	_, err := PackAuthorizeKey(
		common.HexToAddress("0x1"),
		SigTypeSecp256k1,
		KeyRestrictions{
			Expiry:        0,
			EnforceLimits: true,
		},
	)
	if err == nil {
		t.Errorf("expected error for Expiry=0 + EnforceLimits=true")
	}
}

func TestPathUSDDailyLimit(t *testing.T) {
	got := PathUSDDailyLimit(20_000_000)
	if got.Amount.Cmp(big.NewInt(20_000_000)) != 0 {
		t.Errorf("amount = %v, want 20_000_000", got.Amount)
	}
	if got.Period != 86400 {
		t.Errorf("period = %d, want 86400", got.Period)
	}
	if got.Token != common.HexToAddress(PathUSDAddress) {
		t.Errorf("token = %s, want %s", got.Token.Hex(), PathUSDAddress)
	}
}

func TestTransferOnlySelectorMatchesERC20(t *testing.T) {
	// transfer(address,uint256) selector is 0xa9059cbb — pinned constant
	// from the ERC-20 spec. Verifies our constant matches the canonical
	// keccak by recomputing it.
	want := []byte{0xa9, 0x05, 0x9c, 0xbb}
	got := TransferOnly(common.HexToAddress(PathUSDAddress)).Selector
	if !bytes.Equal(got[:], want) {
		t.Errorf("selector = %x, want %x", got, want)
	}

	// Recompute from the signature string for safety.
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte("transfer(address,uint256)"))
	derived := hash.Sum(nil)[:4]
	if !bytes.Equal(derived, want) {
		t.Errorf("derived selector %x doesn't match canonical %x — go-ethereum keccak broken?",
			derived, want)
	}
}
