package webhooks

import "context"

// LimitEmitter adapts a Publisher to the interface limits.Service expects.
//
// The limits package defines a narrow ViolationEmitter interface so it
// doesn't have to import this package (which would close an import cycle
// the moment webhooks needs to consult a policy). NewLimitEmitter is the
// glue in server wiring: pass the shared Publisher in, get an emitter
// out, hand it to limits.New().
type LimitEmitter struct {
	pub *Publisher
}

// NewLimitEmitter wraps a Publisher.
func NewLimitEmitter(pub *Publisher) *LimitEmitter {
	return &LimitEmitter{pub: pub}
}

// EmitLimitExceeded fan-outs a limit.exceeded event to all subscribers of
// the tenant. The event payload is a flat object — purpose-built for
// dashboards that want to grep for "agent X just got blocked".
//
// All fields land in event_data verbatim. The cross-refs (wallet,
// session key) ALSO go into webhook_events.related_* columns so the
// per-resource event timeline finds them without scanning JSON.
func (e *LimitEmitter) EmitLimitExceeded(
	ctx context.Context,
	tenantID, policyID, violationType, walletID, sessionKeyID,
	attemptedAmount, asset, recipient string,
) {
	if e == nil || e.pub == nil {
		return
	}
	payload := map[string]any{
		"policy_id":           policyID,
		"violation_type":      violationType,
		"wallet_id":           walletID,
		"session_key_id":      sessionKeyID,
		"attempted_amount":    attemptedAmount,
		"asset":               asset,
		"attempted_recipient": recipient,
	}
	// Fire and forget; webhook persistence errors must not break the
	// caller (limits.Service.Check) — the deny verdict stands.
	_, _ = e.pub.Publish(ctx, tenantID, EventLimitExceeded, payload, ResourceRefs{
		WalletID:     walletID,
		SessionKeyID: sessionKeyID,
	})
}
