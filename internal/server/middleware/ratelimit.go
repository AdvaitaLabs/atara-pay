package middleware

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// Per-tenant fixed-window rate limit implemented in Redis.
//
// We use INCR + EXPIRE inside a TxPipeline: simple, atomic enough for a
// fixed-window counter, and survives multiple gateway replicas because
// the state lives in Redis. The fixed window has a known edge-case (two
// bursts straddling the boundary can equal 2×limit briefly) — acceptable
// for the per-tenant scope at MVP rates. A sliding window or token
// bucket lands in M13.2 if traffic patterns demand it.
//
// Routes can opt out by mounting WITHOUT this middleware (e.g. /metrics).
// Unauthenticated requests (no tenant context) are pass-through; the
// auth middleware always runs first.

// RateLimitConfig configures the limiter. Zero values fall back to
// sensible defaults: 600 req/min per tenant + key prefix "rl".
type RateLimitConfig struct {
	RequestsPerMinute int
	KeyPrefix         string
}

// RateLimit returns a Fiber middleware enforcing the configured per-tenant
// budget against Redis. If rdb is nil OR no tenant_id is attached to the
// request (public endpoint), the limit is skipped — public endpoints are
// rate-limited at the edge / by a different layer.
func RateLimit(rdb *redis.Client, cfg RateLimitConfig) fiber.Handler {
	if cfg.RequestsPerMinute <= 0 {
		cfg.RequestsPerMinute = 600
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "rl"
	}
	if rdb == nil {
		return func(c *fiber.Ctx) error { return c.Next() }
	}

	limit := cfg.RequestsPerMinute
	prefix := cfg.KeyPrefix

	return func(c *fiber.Ctx) error {
		tenantID := TenantID(c)
		if tenantID == "" {
			return c.Next()
		}
		windowKey := currentWindowKey(prefix, tenantID, time.Now())

		ctx, cancel := context.WithTimeout(c.UserContext(), 200*time.Millisecond)
		defer cancel()

		// One round-trip: INCR + first-time-EXPIRE.
		var (
			incr *redis.IntCmd
		)
		pipe := rdb.TxPipeline()
		incr = pipe.Incr(ctx, windowKey)
		pipe.Expire(ctx, windowKey, 70*time.Second) // a hair past the window
		if _, err := pipe.Exec(ctx); err != nil {
			// Redis hiccup → fail OPEN. The decision: never block a real
			// request because the rate limiter itself is sick. Operators
			// see this via the metrics middleware (5xx rate stable) and
			// can react. Hard-fail is M13.2.
			return c.Next()
		}

		used := incr.Val()
		remaining := int64(limit) - used
		if remaining < 0 {
			remaining = 0
		}

		// Always set the standard headers so clients can self-throttle.
		c.Set("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
		c.Set("X-RateLimit-Reset", strconv.FormatInt(nextWindowUnix(time.Now()), 10))

		if used > int64(limit) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": fmt.Sprintf("rate limit exceeded: %d req/min", limit),
			})
		}
		return c.Next()
	}
}

// currentWindowKey buckets the current minute. Format keeps minutes
// monotonic so old keys naturally fall off when their TTL expires.
//
//	rl:tn_01J…:202605221423
func currentWindowKey(prefix, tenantID string, now time.Time) string {
	return prefix + ":" + tenantID + ":" + now.UTC().Format("200601021504")
}

// nextWindowUnix is the start of the next minute window in unix seconds.
// Used in X-RateLimit-Reset so callers know when their budget refills.
func nextWindowUnix(now time.Time) int64 {
	next := now.UTC().Truncate(time.Minute).Add(time.Minute)
	return next.Unix()
}
