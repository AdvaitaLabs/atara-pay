package tempo

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethCrypto "github.com/ethereum/go-ethereum/crypto"
)

// AddressRegistry precompile (TIP-1022). Mints / resolves deterministic
// virtual addresses anchored on a parent wallet + caller-supplied bytes32
// label. Funds sent to the virtual address route to the parent.
const AddressRegistryAddress = "0xFDC0000000000000000000000000000000000000"

const addressRegistryABI = `[
  {
    "name": "registerVirtualAddress",
    "type": "function",
    "stateMutability": "nonpayable",
    "inputs": [
      {"name": "parent", "type": "address"},
      {"name": "label",  "type": "bytes32"}
    ],
    "outputs": [
      {"name": "virtualAddress", "type": "address"}
    ]
  },
  {
    "name": "resolve",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "parent", "type": "address"},
      {"name": "label",  "type": "bytes32"}
    ],
    "outputs": [
      {"name": "virtualAddress", "type": "address"}
    ]
  }
]`

var parsedAddressRegistry abi.ABI

func init() {
	a, err := abi.JSON(strings.NewReader(addressRegistryABI))
	if err != nil {
		panic(fmt.Errorf("tempo: parse AddressRegistry abi: %w", err))
	}
	parsedAddressRegistry = a
}

// LabelToBytes32 hashes a free-form customer label (e.g. "invoice-42",
// "user_alice_123") into the 32-byte slot the precompile expects. We use
// keccak256 so two identical labels always map to the same address.
func LabelToBytes32(label string) [32]byte {
	h := gethCrypto.Keccak256([]byte(label))
	var out [32]byte
	copy(out[:], h)
	return out
}

// ResolveVirtualAddress is the read path — call the precompile's view
// function to ask "what address would label X under parent P be?" without
// writing anything on chain. Customers use this to display a deposit
// address in the dashboard before any incoming deposit has registered the
// label.
//
// Returns the zero address when the label hasn't been registered (depending
// on the precompile's implementation; the consumer should still call
// RegisterVirtualAddress before quoting the address as deposit-ready).
func (a *Adapter) ResolveVirtualAddress(
	ctx context.Context, parent common.Address, label [32]byte,
) (common.Address, error) {
	data, err := parsedAddressRegistry.Pack("resolve", parent, label)
	if err != nil {
		return common.Address{}, fmt.Errorf("pack resolve: %w", err)
	}
	to := common.HexToAddress(AddressRegistryAddress)
	out, err := a.client.Eth().CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("resolve: %w", err)
	}
	res, err := parsedAddressRegistry.Unpack("resolve", out)
	if err != nil {
		return common.Address{}, fmt.Errorf("unpack resolve: %w", err)
	}
	if len(res) == 0 {
		return common.Address{}, errors.New("tempo: empty resolve result")
	}
	addr, ok := res[0].(common.Address)
	if !ok {
		return common.Address{}, errors.New("tempo: unexpected resolve type")
	}
	return addr, nil
}

// RegisterVirtualAddressInput packs everything the orchestrator needs.
// ParentPrivKey is the parent wallet's master key — signed transactions
// authorize the precompile to mint the virtual address under that parent.
type RegisterVirtualAddressInput struct {
	ParentPrivKey []byte
	ParentAddress string
	Label         [32]byte
}

// RegisterVirtualAddress signs + broadcasts a registerVirtualAddress()
// call. Returns the (tx hash, derived virtual address) pair. The address
// is computed eagerly via the local Resolve() call so the caller can
// persist it without waiting for the receipt.
//
// Idempotency: the precompile MUST return the same address for the same
// (parent, label) input. Re-registering an existing label is a no-op
// on-chain — burns a tiny amount of gas but harmless.
func (a *Adapter) RegisterVirtualAddress(
	ctx context.Context, in RegisterVirtualAddressInput,
) (txHash string, virtualAddress common.Address, err error) {
	priv, perr := privateKeyFromBytes(in.ParentPrivKey)
	if perr != nil {
		return "", common.Address{}, fmt.Errorf("register vaddr: %w", perr)
	}
	derived := gethCrypto.PubkeyToAddress(priv.PublicKey)
	if derived != common.HexToAddress(in.ParentAddress) {
		return "", common.Address{}, fmt.Errorf(
			"register vaddr: private key derives %s but parent says %s",
			derived.Hex(), in.ParentAddress,
		)
	}

	// Pre-compute the address via the view function so the API response
	// has it ready immediately.
	vaddr, rerr := a.ResolveVirtualAddress(ctx, derived, in.Label)
	if rerr != nil {
		// Non-fatal: the view path can fail before registration on a
		// fresh testnet. Continue to register; we'll re-resolve after.
		vaddr = common.Address{}
	}

	calldata, err := parsedAddressRegistry.Pack("registerVirtualAddress", derived, in.Label)
	if err != nil {
		return "", common.Address{}, fmt.Errorf("pack register: %w", err)
	}
	to := common.HexToAddress(AddressRegistryAddress)

	nonce, err := a.client.Eth().PendingNonceAt(ctx, derived)
	if err != nil {
		return "", common.Address{}, fmt.Errorf("nonce: %w", err)
	}
	gasPrice, err := a.client.Eth().SuggestGasPrice(ctx)
	if err != nil {
		return "", common.Address{}, fmt.Errorf("gas price: %w", err)
	}
	gasLimit, err := a.client.Eth().EstimateGas(ctx, ethereum.CallMsg{
		From: derived, To: &to, Data: calldata,
	})
	if err != nil {
		gasLimit = 300_000
	}

	// Use the generic builder/sign/broadcast we already have via the
	// gethTypes path — same as AuthorizeKey.
	signed, err := signLegacyTx(
		a.client.ChainID(), priv, nonce, &to, big.NewInt(0), gasLimit, gasPrice, calldata,
	)
	if err != nil {
		return "", common.Address{}, fmt.Errorf("sign: %w", err)
	}
	if err := a.client.Eth().SendTransaction(ctx, signed); err != nil {
		return "", common.Address{}, fmt.Errorf("broadcast: %w", err)
	}

	if (vaddr == common.Address{}) {
		// Re-resolve post-broadcast (also non-fatal — caller can poll).
		if re, rerr := a.ResolveVirtualAddress(ctx, derived, in.Label); rerr == nil {
			vaddr = re
		}
	}
	return signed.Hash().Hex(), vaddr, nil
}
