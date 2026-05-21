package tempo

// SCAFFOLD ONLY — fee_payer signing is M11.2.
//
// TIP-1053 / Tempo Transactions support gasless UX via a sponsor signature
// envelope: the user signs the inner transaction, a sponsor wallet signs an
// outer fee_payer envelope, and the chain charges the sponsor's pathUSD
// instead of the user's. This is exactly what Atara needs so an AI agent
// can transact without holding pathUSD itself.
//
// The actual envelope ABI / RLP layout depends on the deployed Tempo node
// version. Until I have a reproducible signing example from a Tempo
// reference node, I'm refusing to ship guess-coded signing — silently
// producing invalid txs would be worse than no feature at all.
//
// This file ships the typed config + an explicit error path so callers can
// see "sponsor requested but unsupported" instead of mysteriously hitting a
// chain-side revert. Once we have a working signing example, M11.2:
//
//   1. Add the fee_payer ABI shape + signing routine here.
//   2. Plumb SponsorPrivKey through TransferWithKey / AuthorizeKey /
//      RegisterVirtualAddress as an opt-in.
//   3. Remove the ErrSponsorNotImplemented sentinel below.
//
// References to chase before M11.2:
//   - tempoxyz/tempo/tips/tip-1053.md (session auth + fee_payer notes)
//   - tempoxyz/tempo/crates/primitives/src/transaction/tt_signature.rs
//   - Anchor case: copy the wire format from a successful test transaction
//     captured against a Moderato testnet node.

import "errors"

// SponsorConfig configures gasless mode. When SponsorPrivKey is set the
// adapter will (M11.2 onward) wrap user transactions in a fee_payer
// envelope signed by this key, and the chain debits sponsor pathUSD for
// gas. ChainID is inherited from the Adapter's client; no extra plumbing
// here.
type SponsorConfig struct {
	SponsorPrivKey []byte // 32-byte secp256k1
	SponsorAddress string // 0x-prefixed; sanity-checked against derived
}

// ErrSponsorNotImplemented is returned by adapter methods that the caller
// asked to run in sponsored mode while the chain plumbing is still M11.2
// work. A request that sees this should fall back to "user pays" or fail
// the operation cleanly.
var ErrSponsorNotImplemented = errors.New(
	"tempo: sponsor (gasless / fee_payer) signing is not yet implemented — see M11.2",
)
