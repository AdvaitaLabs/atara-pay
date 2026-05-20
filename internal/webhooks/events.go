// Package webhooks owns ATARA-Pay's outbound event delivery: domain events
// → fan-out to subscribed endpoints → HMAC-signed HTTP POST with retry.
//
// Two concrete pieces live here:
//
//	Publisher  — call sites (handlers, rotator) invoke Publish() to record
//	             that a domain event happened. We resolve subscribers and
//	             INSERT one webhook_events row per (event × endpoint).
//	             Returns instantly; no HTTP is done here.
//
//	Worker     — background goroutine that drains the queue. Atomic claim
//	             via MarkWebhookEventDelivering, HTTP POST with HMAC, mark
//	             delivered or failed with exponential backoff. Auto-pauses
//	             endpoints whose receiver stays broken too long.
//
// Event names follow the "resource.action" convention. Customers subscribe
// to a list of names (or "" = subscribe to all) in
// webhook_endpoints.subscribed_events.
package webhooks

// Canonical event types. Adding a new one is one line here + one call to
// Publisher.Publish at the site that emits it. The list is intentionally
// flat (no nested types or hierarchies) so the matching loop stays a
// trivial string compare.
const (
	// Onramp lifecycle.
	EventOnrampCompleted = "onramp.completed"
	EventOnrampFailed    = "onramp.failed"

	// Transfer lifecycle.
	EventTransactionSucceeded = "transaction.succeeded"
	EventTransactionFailed    = "transaction.failed"

	// Session key lifecycle.
	EventSessionKeyCreated = "session_key.created"
	EventSessionKeyExpired = "session_key.expired"
	EventSessionKeyRevoked = "session_key.revoked"
	EventSessionKeyRotated = "session_key.rotated"

	// Limit enforcement.
	EventLimitExceeded = "limit.exceeded"

	// Wallet group lifecycle (rarely used; placeholder for parity).
	EventWalletGroupCreated = "wallet_group.created"
)

// ResourceRefs is the cross-reference cluster every event carries so the
// dashboard's "events on this wallet / tx / order" timeline can index
// without scanning event_data JSON. All fields optional; only the ones
// relevant to the event get populated.
type ResourceRefs struct {
	WalletID       string
	GroupID        string
	TransactionID  string
	OnrampOrderID  string
	SessionKeyID   string
}
