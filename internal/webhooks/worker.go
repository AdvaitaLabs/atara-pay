package webhooks

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// Worker is the delivery loop. One per process is plenty for MVP; multiple
// workers can run safely because MarkWebhookEventDelivering atomically
// claims a row before any HTTP work begins.
type Worker struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries

	http *http.Client
	tick time.Duration

	// Per-cycle worklimit. Higher values smooth out latency spikes but
	// keep cycles longer.
	batch int32

	// After this many attempts (initial + retries) we declare the event
	// dead. With the default backoff below this is ~31 hours total.
	maxAttempts int32

	// Auto-pause an endpoint after this many CONSECUTIVE failures across
	// all of its events. Prevents a permanently-broken receiver from
	// dragging down our worker forever.
	pauseAfterConsecutive int32

	// onDelivered/onFailed hooks for tests + metrics.
	onTickFn func(TickStats)
}

// TickStats captures one cycle's work. Exposed for tests / observability.
type TickStats struct {
	Claimed   int
	Delivered int
	Failed    int
	Dead      int
}

// NewWorker builds a Worker with sane defaults. Pass 0 to take any default.
func NewWorker(pool *pgxpool.Pool) *Worker {
	return &Worker{
		pool: pool,
		q:    sqlcgen.New(pool),
		http: &http.Client{
			Timeout: 10 * time.Second,
			// Disable HTTP/2 push, follow redirects up to 3 hops.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		tick:                  5 * time.Second,
		batch:                 200,
		maxAttempts:           8,
		pauseAfterConsecutive: 30,
	}
}

// SetOnTick attaches a hook fired after each cycle. Test convenience.
func (w *Worker) SetOnTick(fn func(TickStats)) { w.onTickFn = fn }

// Run blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()
	log.Printf("[webhooks] worker started (tick=%s, batch=%d, max_attempts=%d)",
		w.tick, w.batch, w.maxAttempts)
	for {
		select {
		case <-ctx.Done():
			log.Println("[webhooks] worker stopped")
			return
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// Tick processes one batch. Exported so tests + admin tools can drive
// synchronously.
func (w *Worker) Tick(ctx context.Context) TickStats {
	var stats TickStats
	now := time.Now().UTC()

	due, err := w.q.ListDueWebhookEvents(ctx, sqlcgen.ListDueWebhookEventsParams{
		NextAttemptAt: pgtype.Timestamptz{Time: now, Valid: true},
		Limit:         w.batch,
	})
	if err != nil {
		log.Printf("[webhooks] list due: %v", err)
		return stats
	}

	for _, evt := range due {
		// Atomic claim: only one worker wins this row.
		claimed, err := w.q.MarkWebhookEventDelivering(ctx, evt.ID)
		if err != nil {
			// Another worker beat us to it, or the row already moved out
			// of pending/failed. Skip silently.
			continue
		}
		stats.Claimed++

		w.deliverOne(ctx, claimed, &stats)
	}

	if w.onTickFn != nil {
		w.onTickFn(stats)
	}
	return stats
}

// deliverOne executes the full HTTP attempt for a single event.
func (w *Worker) deliverOne(
	ctx context.Context, evt sqlcgen.WebhookEvent, stats *TickStats,
) {
	if !evt.EndpointID.Valid {
		// Endpoint deleted between publish and delivery. Mark dead so
		// the row stops cycling.
		_ = w.q.MarkWebhookEventDead(ctx, evt.ID)
		stats.Dead++
		return
	}

	endpoint, err := w.q.GetWebhookEndpointByID(ctx, evt.EndpointID.String)
	if err != nil || endpoint.Status != "active" {
		// Endpoint paused / deleted. Reschedule far in the future so a
		// later resume picks it up, but don't burn a retry slot here.
		w.scheduleRetry(ctx, evt, 0, "endpoint not active", "", 0)
		stats.Failed++
		return
	}

	// HMAC sign with the secret VERSION the event was published under.
	// During a secret rotation window we keep using the prior version for
	// already-queued events so receivers verifying against the version
	// header succeed.
	signature := Sign(endpoint.Secret, evt.EventData)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		endpoint.Url, bytes.NewReader(evt.EventData))
	if err != nil {
		w.scheduleRetry(ctx, evt, 0, err.Error(), "", 0)
		stats.Failed++
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Atara-Event-Id", evt.ID)
	req.Header.Set("X-Atara-Event-Type", evt.EventType)
	req.Header.Set("X-Atara-Signature", signature)
	if evt.SignatureVersion.Valid {
		req.Header.Set("X-Atara-Signature-Version",
			intToString(int(evt.SignatureVersion.Int16)))
	}

	resp, err := w.http.Do(req)
	if err != nil {
		w.scheduleRetry(ctx, evt, 0, err.Error(), "", 0)
		stats.Failed++
		w.bumpFailure(ctx, endpoint.ID)
		return
	}
	defer resp.Body.Close()

	// Read a truncated body for the audit row — never let a 50MB
	// customer-side error page blow up the column.
	respBody := readTruncated(resp.Body, 2048)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = w.q.MarkWebhookEventDelivered(ctx, sqlcgen.MarkWebhookEventDeliveredParams{
			ID:               evt.ID,
			LastResponseCode: pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
		})
		_ = w.q.TouchWebhookEndpointSuccess(ctx, endpoint.ID)
		stats.Delivered++
		return
	}

	// Non-2xx: schedule a retry (or mark dead if past max attempts).
	w.scheduleRetry(ctx, evt,
		int32(resp.StatusCode),
		"non-2xx response",
		respBody,
		int32(resp.StatusCode),
	)
	stats.Failed++
	w.bumpFailure(ctx, endpoint.ID)
}

// scheduleRetry is the single place that decides "give up or try again".
// All MarkFailed / MarkDead transitions go through here so the math is
// in one spot.
func (w *Worker) scheduleRetry(
	ctx context.Context,
	evt sqlcgen.WebhookEvent,
	httpStatus int32,
	errStr, respBody string,
	respCode int32,
) {
	next := evt.Attempts + 1
	if next >= w.maxAttempts {
		_ = w.q.MarkWebhookEventDead(ctx, evt.ID)
		return
	}
	nextAt := time.Now().UTC().Add(backoffFor(int(next)))
	_, _ = w.q.MarkWebhookEventFailed(ctx, sqlcgen.MarkWebhookEventFailedParams{
		ID:               evt.ID,
		NextAttemptAt:    pgtype.Timestamptz{Time: nextAt, Valid: true},
		LastResponseCode: pgtype.Int4{Int32: respCode, Valid: respCode > 0},
		LastResponseBody: pgxText(respBody),
		LastError:        pgxText(errStr),
	})
}

// bumpFailure increments the endpoint's consecutive-failure counter and
// pauses it once we cross the threshold.
func (w *Worker) bumpFailure(ctx context.Context, endpointID string) {
	cnt, err := w.q.TouchWebhookEndpointFailure(ctx, endpointID)
	if err != nil {
		return
	}
	if cnt >= w.pauseAfterConsecutive {
		_ = w.q.PauseWebhookEndpoint(ctx, endpointID)
		log.Printf("[webhooks] endpoint %s auto-paused after %d consecutive failures",
			endpointID, cnt)
	}
}

// backoffFor maps an attempt number to a delay. Bounded jitterless
// exponential — Atara doesn't care about thundering herds at our scale.
//
//	1 → 30s
//	2 → 2m
//	3 → 10m
//	4 → 1h
//	5 → 6h
//	6 → 24h
//	7+ → 24h (capped)
func backoffFor(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	case 4:
		return 1 * time.Hour
	case 5:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func readTruncated(r io.Reader, max int) string {
	buf := make([]byte, max)
	n, _ := io.ReadFull(r, buf)
	// ReadFull returns io.ErrUnexpectedEOF for short reads but n is correct.
	return string(buf[:n])
}

func intToString(n int) string {
	// Tiny, allocation-light. Avoids fmt.Sprintf in the hot path.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
