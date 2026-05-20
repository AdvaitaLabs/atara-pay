// Package limits enforces ATARA-Pay's spending policies.
//
// The Service.Check() entry point answers ONE question per transaction:
//
//	"Is this transfer allowed under the policy that governs this scope?"
//
// It returns a structured CheckResult so callers can branch cleanly between
// 403 (denied), 200 (allowed), and 500 (transient error). Violations are
// persisted to limit_violations as a side effect on deny — same place that
// decides also records the audit row.
//
// Policy resolution (most-specific → broadest):
//
//	1. session_key   (scope_id = the session key id used to sign)
//	2. wallet        (scope_id = wallet id)
//	3. wallet_group  (scope_id = the wallet's group id)
//	4. tenant_default
//
// First hit wins. If none exists, the transfer is ALLOWED with policy_id =
// "" — no policy means no enforcement. This is intentional for MVP: only
// policies the customer explicitly created take effect.
//
// Period accumulators (daily / weekly / monthly) land in a follow-up commit
// (M4.2b). This file ships the cheap gates (per_tx / recipient / expiry).
package limits

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// Querier is the narrow subset of sqlcgen.Querier this package needs.
// Decoupling keeps tests free of a real DB.
type Querier interface {
	GetTenantDefaultPolicy(ctx context.Context, tenantID string) (sqlcgen.LimitPolicy, error)
	GetPolicyForScope(ctx context.Context, arg sqlcgen.GetPolicyForScopeParams) (sqlcgen.LimitPolicy, error)
	GetWalletByID(ctx context.Context, id string) (sqlcgen.Wallet, error)
	CreateLimitViolation(ctx context.Context, arg sqlcgen.CreateLimitViolationParams) (sqlcgen.LimitViolation, error)
}

// Service is concurrency-safe. Construct once and share across handlers.
type Service struct {
	q     Querier
	redis *redis.Client // optional; nil disables period accumulators
}

// New builds a Service. Pass nil for redis to fall back to "per-tx and
// recipient gates only" — useful during the migration to Redis or in tests.
func New(q Querier, rdb *redis.Client) *Service {
	return &Service{q: q, redis: rdb}
}

// ──────────────────────────────────────────────────────────────────────
// Public API
// ──────────────────────────────────────────────────────────────────────

// CheckRequest is the input to Check(). Empty optional fields turn the
// corresponding lookups off — e.g. SessionKeyID="" skips the session-key
// policy tier.
type CheckRequest struct {
	TenantID     string
	WalletID     string
	SessionKeyID string

	Amount    string // decimal string ("1.25")
	Asset     string // "USDC" — used in violation log + period_asset alignment
	Recipient string // address or "merchant:foo" / wallet id, freeform

	// TransactionID is the in-flight transactions.id we're checking on
	// behalf of. NULL on Check, set when CreateLimitViolation lands.
	TransactionID string
}

// CheckResult is the structured answer.
type CheckResult struct {
	Allowed   bool
	PolicyID  string     // "" when no policy applied
	Violation *Violation // populated iff Allowed == false
}

// Violation describes why a request was rejected.
type Violation struct {
	Type        string // per_tx_exceeded | recipient_denied | …
	LimitValue  string // human-readable cap (e.g. "5.000000")
	CurrentUsed string // empty for per-tx and recipient cases
	Message     string
}

// Check is the single decision entry point. It resolves the governing
// policy, runs every cheap gate, and on deny writes a limit_violations
// row before returning.
func (s *Service) Check(ctx context.Context, req CheckRequest) (*CheckResult, error) {
	pol, found, err := s.resolvePolicy(ctx, req)
	if err != nil {
		return nil, err
	}
	if !found {
		// No policy at this scope = no enforcement.
		return &CheckResult{Allowed: true}, nil
	}

	if !pol.Enabled {
		// Disabled but matched — treat as no policy.
		return &CheckResult{Allowed: true}, nil
	}

	// Gate 1: policy expiry.
	if pol.ExpiresAt.Valid && !pol.ExpiresAt.Time.IsZero() && time.Now().UTC().After(pol.ExpiresAt.Time) {
		return s.reject(ctx, req, pol, "policy_expired",
			pol.ExpiresAt.Time.UTC().Format(time.RFC3339), "",
			"policy expired at "+pol.ExpiresAt.Time.UTC().Format(time.RFC3339))
	}

	// Gate 2: per-transaction cap.
	if pol.PerTxAmount.Valid {
		over, capStr, err := exceedsNumeric(pol.PerTxAmount, req.Amount)
		if err != nil {
			return nil, err
		}
		if over {
			return s.reject(ctx, req, pol, "per_tx_exceeded",
				capStr, req.Amount,
				fmt.Sprintf("amount %s exceeds per-tx cap %s", req.Amount, capStr))
		}
	}

	// Gate 3: recipient lists.
	if v := recipientGate(req.Recipient, pol); v != "" {
		return s.reject(ctx, req, pol, v, "", "",
			fmt.Sprintf("recipient %q rejected by policy", req.Recipient))
	}

	// Gates 4-6: period accumulators (daily / weekly / monthly).
	// Skip cleanly if Redis isn't wired — policies still enforce per-tx /
	// recipient / expiry above. Redis-less mode is documented in PLAN.md
	// as the fallback during the Redis migration window.
	if s.redis != nil {
		if v, err := s.checkPeriods(ctx, req, pol); err != nil {
			return nil, err
		} else if v != nil {
			return v, nil
		}
	}

	return &CheckResult{Allowed: true, PolicyID: pol.ID}, nil
}

// checkPeriods enforces the daily/weekly/monthly caps in order. On the
// first violation it rolls back the INCRBYs done for already-passed
// periods (so today's denied 5.01 USDC doesn't permanently inflate the
// daily counter) and returns a deny result.
func (s *Service) checkPeriods(
	ctx context.Context, req CheckRequest, pol sqlcgen.LimitPolicy,
) (*CheckResult, error) {
	deltaMicros, err := scaleToMicros(req.Amount)
	if err != nil {
		// Treat un-parseable amounts as a bad request; caller already
		// validates shape, so this should only fire on truly malformed
		// inputs. Return nil so the per-tx gate's own error path takes over.
		return nil, fmt.Errorf("limits: %w", err)
	}
	tz := loadTimezone(pol.Timezone)
	now := time.Now().UTC()

	type budget struct {
		p   period
		cap int64
	}
	plan := []budget{
		{periodDaily, numericToMicros(pol.DailyAmount)},
		{periodWeekly, numericToMicros(pol.WeeklyAmount)},
		{periodMonthly, numericToMicros(pol.MonthlyAmount)},
	}

	// Track keys we successfully incremented so we can DECRBY on a later
	// period's failure. (The rollbackBudget struct is package-level so
	// rollbackBudgets can take it cleanly.)
	var rollbacks []rollbackBudget

	for _, b := range plan {
		violationType, _, err := periodBudgetCheck(
			ctx, s.redis, pol.ID, b.p, b.cap, deltaMicros, now, tz,
		)
		if err == errCapMissing {
			continue // no cap on this period
		}
		if err != nil {
			// Redis hiccup: roll back the prior INCRs (best-effort) and
			// propagate so the caller can decide whether to fail-open or
			// fail-closed. Atara fails-closed today.
			s.rollbackBudgets(ctx, rollbacks)
			return nil, err
		}
		if violationType != "" {
			// Roll back the budgets we already incremented this call.
			s.rollbackBudgets(ctx, rollbacks)
			capStr := microsToDecimal(b.cap)
			// Note: currentUsedStr from periodBudgetCheck already excludes
			// this attempt — that's what we want for the audit log.
			currentUsed := microsToDecimal(b.cap) // worst-case display; the
			// upstream call recomputes deductedTotal but we lose that here
			// after rollback. The audit row still pins the cap, which is
			// the actionable number for customers.
			_ = currentUsed
			return s.reject(ctx, req, pol, violationType,
				capStr, "",
				fmt.Sprintf("%s cap %s exceeded", string(b.p), capStr))
		}
		// Allowed for this period. Record the key so a later deny can
		// roll us back.
		rollbacks = append(rollbacks, rollbackBudget{
			key:   fmt.Sprintf("usage:%s:%s", pol.ID, periodKey(b.p, now, tz)),
			delta: deltaMicros,
		})
	}
	return nil, nil
}

// rollbackBudget pairs a Redis key with the delta we INCRBY'd onto it, so
// a later check-failure in the same Check() call can DECRBY back.
type rollbackBudget struct {
	key   string
	delta int64
}

func (s *Service) rollbackBudgets(ctx context.Context, list []rollbackBudget) {
	for _, r := range list {
		_ = s.redis.DecrBy(ctx, r.key, r.delta).Err()
	}
}

// ──────────────────────────────────────────────────────────────────────
// Policy resolution
// ──────────────────────────────────────────────────────────────────────

// resolvePolicy walks the scope chain most-specific → broadest.
// session_key → wallet → wallet_group → tenant_default. Returns (policy,
// true, nil) on a hit, (zero, false, nil) when nothing matches.
func (s *Service) resolvePolicy(
	ctx context.Context, req CheckRequest,
) (sqlcgen.LimitPolicy, bool, error) {
	if req.SessionKeyID != "" {
		if p, err := s.q.GetPolicyForScope(ctx, sqlcgen.GetPolicyForScopeParams{
			TenantID:  req.TenantID,
			ScopeType: "session_key",
			ScopeID:   pgxTextOpt(req.SessionKeyID),
		}); err == nil {
			return p, true, nil
		}
	}

	if req.WalletID != "" {
		if p, err := s.q.GetPolicyForScope(ctx, sqlcgen.GetPolicyForScopeParams{
			TenantID:  req.TenantID,
			ScopeType: "wallet",
			ScopeID:   pgxTextOpt(req.WalletID),
		}); err == nil {
			return p, true, nil
		}
		// Walk up to wallet_group via the wallet row.
		if w, err := s.q.GetWalletByID(ctx, req.WalletID); err == nil {
			if p, err := s.q.GetPolicyForScope(ctx, sqlcgen.GetPolicyForScopeParams{
				TenantID:  req.TenantID,
				ScopeType: "wallet_group",
				ScopeID:   pgxTextOpt(w.GroupID),
			}); err == nil {
				return p, true, nil
			}
		}
	}

	if p, err := s.q.GetTenantDefaultPolicy(ctx, req.TenantID); err == nil {
		return p, true, nil
	}

	return sqlcgen.LimitPolicy{}, false, nil
}

// ──────────────────────────────────────────────────────────────────────
// Gates
// ──────────────────────────────────────────────────────────────────────

// recipientGate implements the allowlist / denylist semantics:
//
//   - allowed_recipients non-empty → strict whitelist mode; missing entry
//     ⇒ "recipient_not_allowlisted".
//   - allowed_recipients empty + denied_recipients non-empty + match ⇒
//     "recipient_denied".
//
// Both lists are JSON arrays of strings stored in the policy row.
// Comparison is case-insensitive on the literal recipient string.
func recipientGate(recipient string, pol sqlcgen.LimitPolicy) string {
	if recipient == "" {
		return ""
	}
	target := strings.ToLower(recipient)

	allow := parseJSONStringList(pol.AllowedRecipients)
	if len(allow) > 0 {
		for _, a := range allow {
			if strings.ToLower(a) == target {
				return ""
			}
		}
		return "recipient_not_allowlisted"
	}
	deny := parseJSONStringList(pol.DeniedRecipients)
	for _, d := range deny {
		if strings.ToLower(d) == target {
			return "recipient_denied"
		}
	}
	return ""
}

// ──────────────────────────────────────────────────────────────────────
// Persistence / helpers
// ──────────────────────────────────────────────────────────────────────

func (s *Service) reject(
	ctx context.Context,
	req CheckRequest,
	pol sqlcgen.LimitPolicy,
	violationType, limitValue, currentUsed, message string,
) (*CheckResult, error) {
	// Best-effort audit write. A violation we couldn't persist is still a
	// real denial — caller must reject either way.
	_, _ = s.q.CreateLimitViolation(ctx, sqlcgen.CreateLimitViolationParams{
		TenantID:           req.TenantID,
		PolicyID:           pgxTextOpt(pol.ID),
		WalletID:           pgxTextOpt(req.WalletID),
		SessionKeyID:       pgxTextOpt(req.SessionKeyID),
		TransactionID:      pgxTextOpt(req.TransactionID),
		ViolationType:      violationType,
		AttemptedAsset:     pgxTextOpt(req.Asset),
		AttemptedRecipient: pgxTextOpt(req.Recipient),
		Metadata:           []byte("{}"),
	})
	return &CheckResult{
		Allowed:  false,
		PolicyID: pol.ID,
		Violation: &Violation{
			Type:        violationType,
			LimitValue:  limitValue,
			CurrentUsed: currentUsed,
			Message:     message,
		},
	}, nil
}

// exceedsNumeric returns true iff amount > cap (both as decimal strings).
// Tolerates an empty cap by returning false. The cap is reduced to a
// string for the violation log.
func exceedsNumeric(cap interface{}, amount string) (bool, string, error) {
	capStr := numericToString(cap)
	if capStr == "" {
		return false, "", nil
	}
	capDec, ok := new(big.Float).SetString(capStr)
	if !ok {
		return false, capStr, errors.New("limits: malformed cap value")
	}
	amtDec, ok := new(big.Float).SetString(amount)
	if !ok {
		return false, capStr, errors.New("limits: malformed amount")
	}
	return amtDec.Cmp(capDec) > 0, capStr, nil
}
