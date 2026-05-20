package middleware

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/atara-xyz/atara-pay/internal/metrics"
)

// Metrics returns a Fiber middleware that increments the HTTP counter and
// records request latency. The route label uses fiber's Route().Path so we
// emit `/v1/wallet-groups/:id/transactions` instead of the substituted
// path — cardinality stays bounded by route count, not user ids.
//
// Pass nil for r to disable instrumentation cleanly (useful in tests).
func Metrics(r *metrics.Registry) fiber.Handler {
	if r == nil {
		return func(c *fiber.Ctx) error { return c.Next() }
	}
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		dur := time.Since(start).Seconds()

		route := c.Route().Path
		if route == "" {
			route = "unknown"
		}
		method := c.Method()
		status := c.Response().StatusCode()

		r.HTTPRequests.WithLabelValues(method, route, metrics.StatusClass(status)).Inc()
		r.HTTPDuration.WithLabelValues(method, route).Observe(dur)

		// Carry the status code via the response header for tests / debug.
		_ = strconv.Itoa(status) // kept for future use; intentional no-op now
		return err
	}
}
