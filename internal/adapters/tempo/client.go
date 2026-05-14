// Package tempo implements the ATARA-Pay Adapter for the Tempo L1 network.
//
// Tempo is a sub-second-finality EVM L1 with:
//   - JSON-RPC fully Ethereum-compatible
//   - TIP-20 stablecoins (pathUSD) at deterministic precompile addresses
//   - Native fee sponsorship + session keys
//
// References:
//   - mainnet RPC: https://rpc.presto.tempo.xyz   (chainId 4217)
//   - testnet RPC: https://rpc.moderato.tempo.xyz (chainId 42431)
package tempo

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	// MainnetChainID is the Tempo mainnet (Presto) chain ID.
	MainnetChainID int64 = 4217
	// TestnetChainID is the Tempo testnet (Moderato) chain ID.
	TestnetChainID int64 = 42431

	// MainnetRPC and TestnetRPC are the public RPC endpoints.
	MainnetRPC = "https://rpc.presto.tempo.xyz"
	TestnetRPC = "https://rpc.moderato.tempo.xyz"

	dialTimeout = 10 * time.Second
)

// Config configures the Tempo adapter.
type Config struct {
	// RPCURL is required. Use MainnetRPC, TestnetRPC, or a self-hosted node.
	RPCURL string

	// ChainID is required. Pass MainnetChainID, TestnetChainID, or a custom id.
	ChainID int64
}

// Client is a thin wrapper around go-ethereum's ethclient that carries the
// Tempo chain id and exposes only the methods the adapter needs.
type Client struct {
	eth     *ethclient.Client
	chainID *big.Int
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.RPCURL == "" {
		return nil, fmt.Errorf("tempo: RPCURL is required")
	}
	if cfg.ChainID == 0 {
		return nil, fmt.Errorf("tempo: ChainID is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	c, err := ethclient.DialContext(ctx, cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("tempo: dial %s: %w", cfg.RPCURL, err)
	}

	return &Client{eth: c, chainID: big.NewInt(cfg.ChainID)}, nil
}

func (c *Client) Eth() *ethclient.Client { return c.eth }
func (c *Client) ChainID() *big.Int      { return new(big.Int).Set(c.chainID) }
