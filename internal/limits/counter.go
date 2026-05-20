package limits

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Period accounting via Redis INCR + EXPIREAT.
//
// We scale amounts to "microunits" (×1e6) so Redis can use INCRBY (int64).
// 1e6 fits TIP-20 / USDC precision (6 decimals); the largest legitimate
// period cap we expect is in the millions of USD, well under int64 max
// (~9.2e18). Above that the limit system breaks — and the customer's
// audit/compliance posture would already require a custom solution.
//
// Atomicity:
//   We INCRBY first, check the new value against the cap, and DECRBY back
//   on bust. Between INCR and DECR a concurrent reader might briefly see
//   an inflated count (over-reject), which is the safer failure mode than
//   under-reject. A future Lua-script upgrade collapses the round-trip
//   into one atomic op when contention becomes measurable.

// microScale is the integer multiplier we apply to convert decimal-string
// amounts into int64 microunits.
const microScale = 1_000_000

// errCapMissing means the policy has no cap for this period — caller skips.
var errCapMissing = errors.New("limits: period cap not configured")

// scaleToMicros parses a decimal string and returns int64 microunits.
// "1.25" → 1250000. Rejects amounts with more than 6 fractional digits
// (the 7th digit and beyond would be silently truncated).
func scaleToMicros(decimal string) (int64, error) {
	decimal = strings.TrimSpace(decimal)
	if decimal == "" {
		return 0, errors.New("limits: empty amount")
	}
	neg := strings.HasPrefix(decimal, "-")
	if neg {
		return 0, errors.New("limits: negative amount")
	}

	intPart, fracPart, hasDot := strings.Cut(decimal, ".")
	if !hasDot {
		fracPart = ""
	}
	if len(fracPart) > 6 {
		return 0, fmt.Errorf("limits: amount %q has more than 6 fractional digits", decimal)
	}
	fracPart += strings.Repeat("0", 6-len(fracPart))

	combined := strings.TrimLeft(intPart+fracPart, "0")
	if combined == "" {
		return 0, nil
	}
	n, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return 0, fmt.Errorf("limits: amount %q is not numeric", decimal)
	}
	if !n.IsInt64() {
		return 0, fmt.Errorf("limits: amount %q overflows int64 microunits", decimal)
	}
	return n.Int64(), nil
}

// microsToDecimal reverses scaleToMicros for the violation/audit log.
func microsToDecimal(m int64) string {
	if m == 0 {
		return "0"
	}
	intPart := m / microScale
	frac := m % microScale
	if frac == 0 {
		return fmt.Sprintf("%d", intPart)
	}
	s := fmt.Sprintf("%d.%06d", intPart, frac)
	return strings.TrimRight(s, "0")
}

// periodBudgetCheck enforces ONE period's cap. Returns nil for "allowed";
// (violationType, currentUsedStr) for "denied"; errCapMissing if the
// policy doesn't cap this period (caller skips).
//
// On allow it leaves the counter incremented; on deny it rolls back to
// preserve correctness.
func periodBudgetCheck(
	ctx context.Context, rdb *redis.Client,
	policyID string, p period, capMicros int64, deltaMicros int64,
	now time.Time, tz *time.Location,
) (violationType, currentUsedStr string, err error) {
	if capMicros <= 0 {
		return "", "", errCapMissing
	}

	key := fmt.Sprintf("usage:%s:%s", policyID, periodKey(p, now, tz))
	expireAt := nextReset(p, now, tz)

	pipe := rdb.TxPipeline()
	incr := pipe.IncrBy(ctx, key, deltaMicros)
	pipe.ExpireAt(ctx, key, expireAt)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", "", fmt.Errorf("limits: redis incr %s: %w", string(p), err)
	}
	total := incr.Val()

	if total > capMicros {
		// Roll back the INCRBY. If this DECRBY fails (e.g. Redis flaked) we
		// accept the inflated count — better to over-reject than to leak a
		// payment past the cap.
		_ = rdb.DecrBy(ctx, key, deltaMicros).Err()
		return string(p) + "_exceeded", microsToDecimal(total - deltaMicros), nil
	}
	return "", microsToDecimal(total), nil
}

// numericToMicros consolidates pgtype.Numeric / string handling for caps.
// Returns 0 when the cap column is NULL or zero — periodBudgetCheck treats
// that as "no cap on this period".
func numericToMicros(v interface{}) int64 {
	s := numericToString(v)
	if s == "" {
		return 0
	}
	m, err := scaleToMicros(s)
	if err != nil || m < 0 {
		return 0
	}
	return m
}
