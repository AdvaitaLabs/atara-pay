package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
)

// Publisher is concurrency-safe. Build one per process and share.
type Publisher struct {
	q *sqlcgen.Queries
}

func NewPublisher(pool *pgxpool.Pool) *Publisher {
	return &Publisher{q: sqlcgen.New(pool)}
}

// Publish records a domain event and fans it out to every active endpoint
// of this tenant that subscribes to the event type.
//
// Returns the number of webhook_events rows inserted (= subscribers
// matched). 0 is a valid result — the customer simply has no receivers
// subscribed. The call NEVER fails the caller's request: any persistence
// error is logged via the returned err but is the caller's choice to
// propagate or swallow.
//
// data is marshaled to JSON and stored in webhook_events.event_data. Pass
// any shape — a typed struct, a map, whatever. The delivery worker sends
// it as the raw body of the HTTP POST.
func (p *Publisher) Publish(
	ctx context.Context,
	tenantID, eventType string,
	data any,
	refs ResourceRefs,
) (int, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return 0, fmt.Errorf("webhooks: marshal event_data: %w", err)
	}

	endpoints, err := p.q.ListActiveEndpointsForTenant(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("webhooks: list subscribers: %w", err)
	}

	inserted := 0
	for _, ep := range endpoints {
		if !endpointSubscribed(ep, eventType) {
			continue
		}
		if _, err := p.q.CreateWebhookEvent(ctx, sqlcgen.CreateWebhookEventParams{
			ID:                    id.New(id.PrefixWebhookEvent),
			TenantID:              tenantID,
			EndpointID:            pgxText(ep.ID),
			EventType:             eventType,
			EventData:             body,
			RelatedWalletID:       pgxText(refs.WalletID),
			RelatedGroupID:        pgxText(refs.GroupID),
			RelatedTransactionID:  pgxText(refs.TransactionID),
			RelatedOnrampOrderID:  pgxText(refs.OnrampOrderID),
			RelatedSessionKeyID:   pgxText(refs.SessionKeyID),
			Status:                "pending",
			Attempts:              0,
			NextAttemptAt:         pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			SignatureVersion:      pgtype.Int2{Int16: ep.SecretVersion, Valid: true},
			Metadata:              []byte("{}"),
		}); err != nil {
			// Skip this endpoint but keep going — one bad subscriber
			// shouldn't suppress delivery to the others.
			continue
		}
		inserted++
	}
	return inserted, nil
}

// endpointSubscribed returns true iff the endpoint subscribes to
// eventType. An empty subscribed_events list means "all events".
func endpointSubscribed(ep sqlcgen.WebhookEndpoint, eventType string) bool {
	if len(ep.SubscribedEvents) == 0 || string(ep.SubscribedEvents) == "[]" {
		return true
	}
	var list []string
	if err := json.Unmarshal(ep.SubscribedEvents, &list); err != nil {
		return false
	}
	for _, s := range list {
		if s == eventType {
			return true
		}
	}
	return false
}

// pgxText mirrors handlers.pgxText — kept local so the webhooks package
// has no handlers dependency.
func pgxText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
