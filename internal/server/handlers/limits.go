package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/id"
	"github.com/atara-xyz/atara-pay/internal/limits"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
)

// Limits handles the tenant-level limit endpoints:
//   GET    /v1/tenants/me/limits             — current tenant_default policy
//   PUT    /v1/tenants/me/limits             — switch tier or set custom caps
//   GET    /v1/tenants/me/limits/violations  — recent rejections
type Limits struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewLimits(pool *pgxpool.Pool) *Limits {
	return &Limits{pool: pool, q: sqlcgen.New(pool)}
}

// ──────────────────────────────────────────────────────────────────────
// Views
// ──────────────────────────────────────────────────────────────────────

// PolicyView is the safe-to-return projection of a limit_policies row.
type PolicyView struct {
	ID           string `json:"id"`
	ScopeType    string `json:"scope_type"`
	Tier         string `json:"tier,omitempty"`
	PerTxAmount  string `json:"per_tx_amount,omitempty"`
	DailyAmount  string `json:"daily_amount,omitempty"`
	WeeklyAmount string `json:"weekly_amount,omitempty"`
	MonthlyAmount string `json:"monthly_amount,omitempty"`
	PeriodAsset  string `json:"period_asset"`
	Timezone     string `json:"timezone"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// ViolationView is the safe-to-return projection of a limit_violations row.
type ViolationView struct {
	ID                 int64  `json:"id"`
	Type               string `json:"type"`
	PolicyID           string `json:"policy_id,omitempty"`
	WalletID           string `json:"wallet_id,omitempty"`
	SessionKeyID       string `json:"session_key_id,omitempty"`
	TransactionID      string `json:"transaction_id,omitempty"`
	AttemptedAmount    string `json:"attempted_amount,omitempty"`
	AttemptedAsset     string `json:"attempted_asset,omitempty"`
	AttemptedRecipient string `json:"attempted_recipient,omitempty"`
	CreatedAt          string `json:"created_at,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────
// GET /v1/tenants/me/limits
// ──────────────────────────────────────────────────────────────────────

func (h *Limits) GetTenantLimits(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	pol, err := h.q.GetTenantDefaultPolicy(c.UserContext(), tenantID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "no tenant_default policy set",
		})
	}
	return c.JSON(toPolicyView(pol))
}

// ──────────────────────────────────────────────────────────────────────
// PUT /v1/tenants/me/limits
// ──────────────────────────────────────────────────────────────────────

// UpdateTenantLimitsRequest accepts EITHER a tier preset OR explicit caps,
// not both. Tier name short-circuits to the matching limits.Tier preset;
// explicit caps demand all four amounts (per-tx / daily / weekly / monthly)
// so we never leave a column in an inconsistent state.
type UpdateTenantLimitsRequest struct {
	Tier string `json:"tier,omitempty"`

	PerTxAmount   string `json:"per_tx_amount,omitempty"`
	DailyAmount   string `json:"daily_amount,omitempty"`
	WeeklyAmount  string `json:"weekly_amount,omitempty"`
	MonthlyAmount string `json:"monthly_amount,omitempty"`
}

func (h *Limits) UpdateTenantLimits(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}

	// Role gate: only owner/admin can change tier or caps. API-key auth
	// bypasses (key already proves tenant ownership).
	if role := middleware.Role(c); role != "" && role != "owner" && role != "admin" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "your role cannot change tenant limits",
		})
	}

	var req UpdateTenantLimitsRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}

	tier, caps, err := resolveLimitsUpdate(req)
	if err != nil {
		return badRequest(c, err.Error())
	}

	// Replace by disabling old + inserting new. We never UPDATE the active
	// row's caps because limit_violations.policy_id references it — if we
	// mutated the row in place, future limit checks against the same policy
	// id would compare today's cap against yesterday's violation. Inserting
	// a fresh row keeps the audit chain coherent.
	ctx := c.UserContext()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return internalError(c, err)
	}
	defer tx.Rollback(context.Background())
	qtx := h.q.WithTx(tx)

	// Best-effort: disable any existing tenant_default. Missing row is fine.
	if existing, derr := qtx.GetTenantDefaultPolicy(ctx, tenantID); derr == nil {
		if _, ferr := qtx.DisableLimitPolicy(ctx, existing.ID); ferr != nil {
			return internalError(c, ferr)
		}
	}

	tierName := "custom"
	if tier != nil {
		tierName = tier.Name
	}

	pol, err := qtx.CreateLimitPolicy(ctx, sqlcgen.CreateLimitPolicyParams{
		ID:                id.New(id.PrefixLimitPolicy),
		TenantID:          tenantID,
		ScopeType:         "tenant_default",
		ScopeID:           pgxText(""),
		PerTxAmount:       caps.perTx,
		PerTxAsset:        pgxText("USDC"),
		DailyAmount:       caps.daily,
		WeeklyAmount:      caps.weekly,
		MonthlyAmount:     caps.monthly,
		PeriodAsset:       "USDC",
		Timezone:          "UTC",
		ResetDayOfWeek:    1,
		ResetDayOfMonth:   1,
		AllowedRecipients: []byte("[]"),
		DeniedRecipients:  []byte("[]"),
		Enabled:           true,
		Metadata:          []byte(fmt.Sprintf(`{"tier":%q}`, tierName)),
	})
	if err != nil {
		return internalError(c, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return internalError(c, err)
	}
	return c.JSON(toPolicyView(pol))
}

// resolveLimitsUpdate validates the request and produces a (tier?, caps)
// pair. Exactly one of the two modes must be specified.
func resolveLimitsUpdate(req UpdateTenantLimitsRequest) (*limits.Tier, capQuad, error) {
	hasTier := req.Tier != ""
	hasCustom := req.PerTxAmount != "" || req.DailyAmount != "" ||
		req.WeeklyAmount != "" || req.MonthlyAmount != ""

	if hasTier && hasCustom {
		return nil, capQuad{}, errors.New(
			"specify either tier or explicit caps, not both")
	}
	if !hasTier && !hasCustom {
		return nil, capQuad{}, errors.New(
			"specify either tier or all four explicit caps")
	}

	if hasTier {
		switch req.Tier {
		case "conservative":
			t := limits.TierConservative
			return &t, capsFromTier(t), nil
		case "standard":
			t := limits.TierStandard
			return &t, capsFromTier(t), nil
		case "aggressive":
			t := limits.TierAggressive
			return &t, capsFromTier(t), nil
		default:
			return nil, capQuad{},
				fmt.Errorf("unknown tier %q (must be conservative|standard|aggressive)", req.Tier)
		}
	}

	// Custom: require ALL four caps to avoid half-updates.
	if req.PerTxAmount == "" || req.DailyAmount == "" ||
		req.WeeklyAmount == "" || req.MonthlyAmount == "" {
		return nil, capQuad{},
			errors.New("custom caps require per_tx, daily, weekly, and monthly all present")
	}
	caps, err := capsFromStrings(req)
	if err != nil {
		return nil, capQuad{}, err
	}
	return nil, caps, nil
}

// capQuad is the four cap pgtype.Numerics this handler builds and the SQL
// layer consumes. Centralized so resolveLimitsUpdate has one return shape.
type capQuad struct {
	perTx   pgtype.Numeric
	daily   pgtype.Numeric
	weekly  pgtype.Numeric
	monthly pgtype.Numeric
}

func capsFromTier(t limits.Tier) capQuad {
	return capQuad{
		perTx:   limits.ToNumeric(t.PerTxUSD),
		daily:   limits.ToNumeric(t.DailyUSD),
		weekly:  limits.ToNumeric(t.WeeklyUSD),
		monthly: limits.ToNumeric(t.MonthlyUSD),
	}
}

func capsFromStrings(req UpdateTenantLimitsRequest) (capQuad, error) {
	perTx, err := parseDecimalUSD(req.PerTxAmount)
	if err != nil {
		return capQuad{}, fmt.Errorf("per_tx_amount: %w", err)
	}
	daily, err := parseDecimalUSD(req.DailyAmount)
	if err != nil {
		return capQuad{}, fmt.Errorf("daily_amount: %w", err)
	}
	weekly, err := parseDecimalUSD(req.WeeklyAmount)
	if err != nil {
		return capQuad{}, fmt.Errorf("weekly_amount: %w", err)
	}
	monthly, err := parseDecimalUSD(req.MonthlyAmount)
	if err != nil {
		return capQuad{}, fmt.Errorf("monthly_amount: %w", err)
	}
	return capQuad{
		perTx:   limits.ToNumeric(perTx),
		daily:   limits.ToNumeric(daily),
		weekly:  limits.ToNumeric(weekly),
		monthly: limits.ToNumeric(monthly),
	}, nil
}

// parseDecimalUSD coerces a customer-supplied USD string to a whole int64.
// We accept "100" / "100.00" but require integer USDC for the MVP; sub-USD
// caps land in M9 once the dashboard UI for them ships.
func parseDecimalUSD(s string) (int64, error) {
	var whole int64
	_, err := fmt.Sscanf(s, "%d", &whole)
	if err != nil || whole < 0 {
		return 0, errors.New("must be a non-negative integer USD amount")
	}
	if whole > 10_000_000 {
		// Atara's risk team holds the upper bound below this until tier
		// approval flow lands. 10M USDC daily on a fresh customer is well
		// outside expected behavior.
		return 0, errors.New("amount too large; contact support for higher caps")
	}
	return whole, nil
}

// ──────────────────────────────────────────────────────────────────────
// GET /v1/tenants/me/limits/violations
// ──────────────────────────────────────────────────────────────────────

type ListViolationsResponse struct {
	Data []ViolationView `json:"data"`
}

func (h *Limits) ListViolations(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	limit := c.QueryInt("limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := c.QueryInt("offset", 0)
	if offset < 0 {
		offset = 0
	}

	rows, err := h.q.ListLimitViolationsByTenant(c.UserContext(), sqlcgen.ListLimitViolationsByTenantParams{
		TenantID: tenantID,
		Limit:    int32(limit),
		Offset:   int32(offset),
	})
	if err != nil {
		return internalError(c, err)
	}
	out := make([]ViolationView, len(rows))
	for i, r := range rows {
		out[i] = toViolationView(r)
	}
	return c.JSON(ListViolationsResponse{Data: out})
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

func toPolicyView(p sqlcgen.LimitPolicy) PolicyView {
	v := PolicyView{
		ID:          p.ID,
		ScopeType:   p.ScopeType,
		PeriodAsset: p.PeriodAsset,
		Timezone:    p.Timezone,
		Enabled:     p.Enabled,
		PerTxAmount: numericString(p.PerTxAmount),
		DailyAmount: numericString(p.DailyAmount),
		WeeklyAmount: numericString(p.WeeklyAmount),
		MonthlyAmount: numericString(p.MonthlyAmount),
	}
	if p.CreatedAt.Valid {
		v.CreatedAt = p.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	// Extract "tier" from metadata if present (cheap shallow scan to avoid
	// pulling in encoding/json for one string).
	v.Tier = extractMetadataTier(p.Metadata)
	return v
}

func toViolationView(v sqlcgen.LimitViolation) ViolationView {
	out := ViolationView{
		ID:   v.ID,
		Type: v.ViolationType,
	}
	if v.PolicyID.Valid {
		out.PolicyID = v.PolicyID.String
	}
	if v.WalletID.Valid {
		out.WalletID = v.WalletID.String
	}
	if v.SessionKeyID.Valid {
		out.SessionKeyID = v.SessionKeyID.String
	}
	if v.TransactionID.Valid {
		out.TransactionID = v.TransactionID.String
	}
	out.AttemptedAmount = numericString(v.AttemptedAmount)
	if v.AttemptedAsset.Valid {
		out.AttemptedAsset = v.AttemptedAsset.String
	}
	if v.AttemptedRecipient.Valid {
		out.AttemptedRecipient = v.AttemptedRecipient.String
	}
	if v.CreatedAt.Valid {
		out.CreatedAt = v.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

// numericString unwraps a pgtype.Numeric to its canonical decimal string.
// Empty for NULL / invalid columns. Mirrors limits.numericToString but kept
// local so the handlers package doesn't import an internal package's
// helper.
func numericString(n interface{}) string {
	type valid interface{ MarshalJSON() ([]byte, error) }
	if m, ok := n.(valid); ok {
		b, err := m.MarshalJSON()
		if err != nil {
			return ""
		}
		s := string(b)
		if s == "null" {
			return ""
		}
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		return s
	}
	return ""
}

// extractMetadataTier hunts the metadata JSON for a "tier" key. We avoid
// a full json.Unmarshal here because metadata may include arbitrary
// customer-controlled JSON we don't want to deserialize on every read.
func extractMetadataTier(raw []byte) string {
	// metadata looks like  {"tier":"conservative"}  in our INSERTs. A
	// substring scan keeps this allocation-free.
	const key = `"tier":"`
	s := string(raw)
	i := indexOf(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	j := indexOf(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
