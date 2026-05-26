package handlers

import (
	"testing"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// TestCapabilitiesFor is the source-of-truth test for the capability matrix.
// When you change capabilitiesFor, update this test in lockstep — front-end
// and SDK consumers key off the bool fields here.
func TestCapabilitiesFor(t *testing.T) {
	cases := []struct {
		name string
		w    sqlcgen.Wallet
		want WalletCapabilities
	}{
		{
			name: "platform tempo: full server-signed flow",
			w:    sqlcgen.Wallet{ID: "w1", Rail: "tempo", Chain: "tempo", Custody: "platform"},
			want: WalletCapabilities{
				WalletID:           "w1",
				Rail:               "tempo",
				Chain:              "tempo",
				Custody:            "platform",
				CanReadBalance:     true,
				CanTransfer:        true,
				CanMintSessionKey:  true,
				// onramp + prepare/submit false on tempo platform
				CanOnramp:          false,
				CanOnrampWhy:       "onramp is only available on the CrossMint rail today",
				CanPrepareSubmit:   false,
				CanPrepareSubmitWhy: "prepare/submit is for user-custody wallets; platform-custody uses .../transactions directly",
			},
		},
		{
			name: "platform crossmint: transfer + onramp",
			w:    sqlcgen.Wallet{ID: "w2", Rail: "crossmint", Chain: "base", Custody: "platform"},
			want: WalletCapabilities{
				WalletID:          "w2",
				Rail:              "crossmint",
				Chain:             "base",
				Custody:           "platform",
				CanReadBalance:    true,
				CanTransfer:       true,
				CanOnramp:         true,
				CanMintSessionKey: true,
				CanPrepareSubmit:  false,
				CanPrepareSubmitWhy: "prepare/submit is for user-custody wallets; platform-custody uses .../transactions directly",
			},
		},
		{
			name: "user tempo: prepare/submit + on-chain session keys (Phase 4)",
			w:    sqlcgen.Wallet{ID: "w3", Rail: "tempo", Chain: "tempo", Custody: "user"},
			want: WalletCapabilities{
				WalletID:          "w3",
				Rail:              "tempo",
				Chain:             "tempo",
				Custody:           "user",
				CanReadBalance:    true,
				CanTransfer:       false,
				CanTransferWhy:    "user-custody wallet: server has no key. Use POST .../transactions/prepare + .../submit",
				CanPrepareSubmit:  true,
				CanOnramp:         false,
				CanOnrampWhy:      "user-custody onramp requires CrossMint linked-external-wallet (Phase 5+)",
				CanMintSessionKey: true, // Phase 4: two-step authorizeKey
			},
		},
		{
			name: "user crossmint: read-only today",
			w:    sqlcgen.Wallet{ID: "w4", Rail: "crossmint", Chain: "base", Custody: "user"},
			want: WalletCapabilities{
				WalletID:             "w4",
				Rail:                 "crossmint",
				Chain:                "base",
				Custody:              "user",
				CanReadBalance:       true,
				CanTransfer:          false,
				CanTransferWhy:       "user-custody wallet: server has no key. Use POST .../transactions/prepare + .../submit",
				CanPrepareSubmit:     false,
				CanPrepareSubmitWhy:  "user-custody crossmint rail not yet wired (Phase 4); only tempo supports prepare/submit today",
				CanOnramp:            false,
				CanOnrampWhy:         "user-custody onramp requires CrossMint linked-external-wallet (Phase 5+)",
				CanMintSessionKey:    false,
				CanMintSessionKeyWhy: "user-custody session keys are tempo-only today; crossmint support is Phase 5+",
			},
		},
		{
			name: "mpc: all writes disabled until M15",
			w:    sqlcgen.Wallet{ID: "w5", Rail: "tempo", Chain: "tempo", Custody: "mpc"},
			want: WalletCapabilities{
				WalletID:             "w5",
				Rail:                 "tempo",
				Chain:                "tempo",
				Custody:              "mpc",
				CanReadBalance:       true,
				CanTransfer:          false,
				CanTransferWhy:       "MPC custody not yet supported (M15)",
				CanPrepareSubmit:     false,
				CanPrepareSubmitWhy:  "MPC custody not yet supported (M15)",
				CanOnramp:            false,
				CanOnrampWhy:         "MPC custody not yet supported (M15)",
				CanMintSessionKey:    false,
				CanMintSessionKeyWhy: "MPC custody not yet supported (M15)",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := capabilitiesFor(tc.w)
			if got != tc.want {
				t.Errorf("capabilitiesFor(%+v):\n got=%+v\nwant=%+v", tc.w, got, tc.want)
			}
		})
	}
}
