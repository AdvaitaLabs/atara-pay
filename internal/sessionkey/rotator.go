package sessionkey

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// Rotator is the background goroutine that keeps session_keys in sync with
// their scheduled lifecycle. Two responsibilities today:
//
//	1. SWEEP EXPIRED — flip rows where expires_at has passed but status
//	   still says active/rotating to 'expired'. After this point any
//	   sign attempt fails with the existing wallet-match guard.
//
//	2. ROTATION DUE — list session keys whose next_rotation_at has
//	   arrived. We DON'T mint replacements here yet. Generating a new
//	   private key on the gateway is useless unless we can hand it to
//	   the customer's agent runtime, which requires the webhook
//	   delivery system (M7). For now Tick logs the due count so
//	   operators see the queue building up; the actual rotation flow
//	   lands once
//	      - webhook_events publish (M7), and
//	      - on-chain authorizeKey (M5.6 / Sponsored Transactions)
//	   are wired.
//
// Run(ctx) is the goroutine entry. It honors context cancellation for
// graceful shutdown.
type Rotator struct {
	pool     *pgxpool.Pool
	q        *sqlcgen.Queries
	tick     time.Duration
	batch    int32
	onTickFn func(stats TickStats) // optional hook for tests / metrics
}

// TickStats captures one cycle's work. Exposed for tests and observability.
type TickStats struct {
	At              time.Time
	ExpiredFlipped  int
	RotationDueSeen int
}

// NewRotator builds a Rotator. tick<=0 defaults to 60s, batch<=0 to 200.
func NewRotator(pool *pgxpool.Pool, tick time.Duration, batch int32) *Rotator {
	if tick <= 0 {
		tick = 60 * time.Second
	}
	if batch <= 0 {
		batch = 200
	}
	return &Rotator{
		pool:  pool,
		q:     sqlcgen.New(pool),
		tick:  tick,
		batch: batch,
	}
}

// SetOnTick attaches a hook the rotator fires after every tick. Useful for
// tests that want to assert the stats without sleeping.
func (r *Rotator) SetOnTick(fn func(TickStats)) {
	r.onTickFn = fn
}

// Run blocks until ctx is cancelled. Each cycle calls Tick(); errors are
// logged but never propagate — a transient DB failure on tick N doesn't
// kill the worker, it just retries on tick N+1.
func (r *Rotator) Run(ctx context.Context) {
	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()

	log.Printf("[sessionkey] rotator started (interval=%s, batch=%d)", r.tick, r.batch)
	for {
		select {
		case <-ctx.Done():
			log.Println("[sessionkey] rotator stopped")
			return
		case <-ticker.C:
			r.Tick(ctx)
		}
	}
}

// Tick runs one cycle. Exported so external tests / admin tools can run
// the work synchronously.
func (r *Rotator) Tick(ctx context.Context) TickStats {
	stats := TickStats{At: time.Now().UTC()}

	// Bounded query budget per tick.
	expired, eerr := r.q.ListExpiredSessionKeys(ctx, sqlcgen.ListExpiredSessionKeysParams{
		ExpiresAt: pgxTickTimestamp(stats.At),
		Limit:     r.batch,
	})
	if eerr != nil {
		log.Printf("[sessionkey] sweep expired: query failed: %v", eerr)
	}
	for _, sk := range expired {
		if _, err := r.q.UpdateSessionKeyStatus(ctx, sqlcgen.UpdateSessionKeyStatusParams{
			ID:     sk.ID,
			Status: "expired",
		}); err != nil {
			log.Printf("[sessionkey] flip %s to expired: %v", sk.ID, err)
			continue
		}
		stats.ExpiredFlipped++
	}

	due, derr := r.q.ListSessionKeysDueForRotation(ctx, sqlcgen.ListSessionKeysDueForRotationParams{
		NextRotationAt: pgxTickTimestamp(stats.At),
		Limit:          r.batch,
	})
	if derr != nil {
		log.Printf("[sessionkey] rotation-due: query failed: %v", derr)
	}
	stats.RotationDueSeen = len(due)
	if stats.RotationDueSeen > 0 {
		// Visible metric so operators notice the backlog while M5.6+ build
		// the actual hand-off path.
		log.Printf("[sessionkey] %d key(s) due for rotation — pending webhook delivery (M7)",
			stats.RotationDueSeen)
	}

	if r.onTickFn != nil {
		r.onTickFn(stats)
	}
	return stats
}
