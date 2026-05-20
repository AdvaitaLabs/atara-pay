// Package id generates ATARA-Pay's resource identifiers.
//
// IDs are ULIDs (Crockford-encoded, 26 chars, time-sortable) prefixed with a
// short tag so humans can recognize the resource type at a glance:
//
//	tn_01J8K…   tenant
//	u_01J8K…    user
//	ak_01J8K…   api_key
//	wg_01J8K…   wallet_group
//	wlt_01J8K…  wallet
//	tx_01J8K…   transaction
//	ord_01J8K…  onramp_order
//	sk_01J8K…   session_key
//	pol_01J8K…  limit_policy
//	we_01J8K…   webhook_endpoint
//	evt_01J8K…  webhook_event
//
// ULIDs sort lexicographically by time, which lines up perfectly with the
// "ORDER BY id DESC" listing queries that show "latest first".
package id

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Common resource prefixes. Use these constants — never hand-type the strings
// at call sites.
const (
	PrefixTenant          = "tn"
	PrefixUser            = "u"
	PrefixAPIKey          = "ak"
	PrefixWalletGroup     = "wg"
	PrefixWallet          = "wlt"
	PrefixTransaction     = "tx"
	PrefixOnrampOrder     = "ord"
	PrefixSessionKey      = "sk"
	PrefixLimitPolicy     = "pol"
	PrefixWebhookEndpoint = "we"
	PrefixWebhookEvent    = "evt"
)

// New returns a new prefixed ULID, e.g. "tn_01J8KQX…".
func New(prefix string) string {
	if prefix == "" {
		panic("id.New called with empty prefix — always pass one of the Prefix* constants")
	}
	u := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)
	return fmt.Sprintf("%s_%s", prefix, u.String())
}
