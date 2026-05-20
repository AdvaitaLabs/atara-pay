package tempo

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	gethCrypto "github.com/ethereum/go-ethereum/crypto"
)

// AuthorizeKeyInput is everything Adapter.AuthorizeKey() needs to build,
// sign, and broadcast the precompile call.
//
// WalletPrivKey is the wallet's MASTER key — the only signer the chain
// will accept for authorizeKey on that wallet. Caller is responsible for
// decrypting it from wallets.encrypted_private_key just before this call
// and zeroing the bytes immediately after.
type AuthorizeKeyInput struct {
	WalletPrivKey []byte // 32-byte secp256k1
	WalletAddress string // 0x-prefixed, for nonce lookup + sanity check

	SessionKeyAddress string        // public address being authorized
	SignatureType     SignatureType // 0 for secp256k1
	Restrictions      KeyRestrictions
}

// AuthorizeKey signs and broadcasts an AccountKeychain.authorizeKey()
// call. Returns the on-chain tx hash. Caller persists the hash in
// session_keys.on_chain_tx_hash and (independently) sets rail_native=true.
//
// keychain gas estimation is famously fiddly on cold-start tokens; we fall
// back to 500k if the node refuses to estimate.
func (a *Adapter) AuthorizeKey(ctx context.Context, in AuthorizeKeyInput) (string, error) {
	priv, err := privateKeyFromBytes(in.WalletPrivKey)
	if err != nil {
		return "", fmt.Errorf("authorize key: %w", err)
	}

	// Defensive: confirm the supplied wallet address actually belongs to
	// this private key. Mismatch = caller passed a different wallet's key
	// — never sign through it.
	derived := gethCrypto.PubkeyToAddress(priv.PublicKey)
	if derived != common.HexToAddress(in.WalletAddress) {
		return "", fmt.Errorf("authorize key: private key derives %s but wallet record says %s",
			derived.Hex(), in.WalletAddress)
	}

	calldata, err := PackAuthorizeKey(
		common.HexToAddress(in.SessionKeyAddress),
		in.SignatureType,
		in.Restrictions,
	)
	if err != nil {
		return "", err
	}

	to := common.HexToAddress(AccountKeychainAddress)

	nonce, err := a.client.Eth().PendingNonceAt(ctx, derived)
	if err != nil {
		return "", fmt.Errorf("authorize key: nonce: %w", err)
	}
	gasPrice, err := a.client.Eth().SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("authorize key: gas price: %w", err)
	}
	gasLimit, err := a.client.Eth().EstimateGas(ctx, ethereum.CallMsg{
		From: derived, To: &to, Data: calldata,
	})
	if err != nil {
		// Precompile gas estimation can refuse cold-start; the fixed
		// fallback is comfortably above what authorizeKey actually costs
		// on Tempo (observed ~150-200k in practice).
		gasLimit = 500_000
	}

	tx := gethTypes.NewTx(&gethTypes.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    big.NewInt(0),
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     calldata,
	})

	signer := gethTypes.LatestSignerForChainID(a.client.ChainID())
	signed, err := gethTypes.SignTx(tx, signer, priv)
	if err != nil {
		return "", fmt.Errorf("authorize key: sign: %w", err)
	}
	if err := a.client.Eth().SendTransaction(ctx, signed); err != nil {
		return "", fmt.Errorf("authorize key: broadcast: %w", err)
	}
	return signed.Hash().Hex(), nil
}
