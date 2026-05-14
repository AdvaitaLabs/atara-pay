package tempo

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Tempo's TIP-20 tokens (e.g. pathUSD) expose the standard ERC-20 surface
// at deterministic precompile addresses with the 0x20C0 prefix.
//
//	pathUSD: 0x20C0000000000000000000000000000000000000
//
// All TIP-20 tokens use 6 decimals.

const (
	PathUSDAddress  = "0x20C0000000000000000000000000000000000000"
	TIP20Decimals   = 6
)

// erc20ABI is the minimal subset of ERC-20 we need.
const erc20ABI = `[
  {"constant":true,"inputs":[{"name":"owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
  {"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"},
  {"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"}
]`

var parsedERC20 abi.ABI

func init() {
	a, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		panic(fmt.Errorf("tempo: parse erc20 abi: %w", err))
	}
	parsedERC20 = a
}

// tokenAddressFor returns the on-chain address for a supported asset on Tempo.
func tokenAddressFor(asset string) (common.Address, error) {
	switch strings.ToUpper(asset) {
	case "PATHUSD":
		return common.HexToAddress(PathUSDAddress), nil
	default:
		return common.Address{}, fmt.Errorf("tempo: unsupported asset %q", asset)
	}
}

// packTransfer ABI-encodes ERC-20 transfer(to, amount).
func packTransfer(to common.Address, amount *big.Int) ([]byte, error) {
	return parsedERC20.Pack("transfer", to, amount)
}

// packBalanceOf ABI-encodes ERC-20 balanceOf(owner).
func packBalanceOf(owner common.Address) ([]byte, error) {
	return parsedERC20.Pack("balanceOf", owner)
}

// unpackBalanceOf decodes a uint256 result from balanceOf.
func unpackBalanceOf(raw []byte) (*big.Int, error) {
	res, err := parsedERC20.Unpack("balanceOf", raw)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return big.NewInt(0), nil
	}
	bal, ok := res[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("tempo: balanceOf returned non-bigint")
	}
	return bal, nil
}
