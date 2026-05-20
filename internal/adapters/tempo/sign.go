package tempo

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
)

// signLegacyTx builds + signs a LegacyTx with the supplied fields.
// Centralises the tx assembly so TransferWithKey / AuthorizeKey /
// RegisterVirtualAddress don't duplicate the same boilerplate.
func signLegacyTx(
	chainID *big.Int,
	priv *ecdsa.PrivateKey,
	nonce uint64,
	to *common.Address,
	value *big.Int,
	gasLimit uint64,
	gasPrice *big.Int,
	data []byte,
) (*gethTypes.Transaction, error) {
	tx := gethTypes.NewTx(&gethTypes.LegacyTx{
		Nonce:    nonce,
		To:       to,
		Value:    value,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
	})
	signer := gethTypes.LatestSignerForChainID(chainID)
	signed, err := gethTypes.SignTx(tx, signer, priv)
	if err != nil {
		return nil, fmt.Errorf("tempo: sign tx: %w", err)
	}
	return signed, nil
}
