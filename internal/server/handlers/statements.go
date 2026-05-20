package handlers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
	"github.com/atara-xyz/atara-pay/internal/server/middleware"
)

// Statements implements the M9 accounting endpoints. All routes are read
// aggregates over existing tables — no new schema, no async jobs. When
// volume warrants it, swap the queries for a pre-materialized
// monthly_statements table (M9.2).
type Statements struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewStatements(pool *pgxpool.Pool) *Statements {
	return &Statements{pool: pool, q: sqlcgen.New(pool)}
}

// ──────────────────────────────────────────────────────────────────────
// Views
// ──────────────────────────────────────────────────────────────────────

// AssetBalanceLine is one (asset, direction) bucket of summed amounts.
type AssetBalanceLine struct {
	Asset       string `json:"asset"`
	Direction   string `json:"direction"`
	TxCount     int64  `json:"tx_count"`
	TotalAmount string `json:"total_amount"`
}

type GroupBalanceResponse struct {
	GroupID string             `json:"group_id"`
	Lines   []AssetBalanceLine `json:"lines"`
}

type OnrampLine struct {
	FiatCurrency    string `json:"fiat_currency"`
	Asset           string `json:"asset"`
	OrderCount      int64  `json:"order_count"`
	FiatTotal       string `json:"fiat_total"`
	CryptoDelivered string `json:"crypto_delivered"`
}

type MonthlyStatement struct {
	Month        string             `json:"month"`         // YYYY-MM
	WindowStart  string             `json:"window_start"`  // RFC 3339
	WindowEnd    string             `json:"window_end"`    // RFC 3339, exclusive
	Transactions []AssetBalanceLine `json:"transactions"`
	Onramp       []OnrampLine       `json:"onramp"`
}

// ──────────────────────────────────────────────────────────────────────
// GET /v1/wallet-groups/{id}/balance
// ──────────────────────────────────────────────────────────────────────

func (h *Statements) GetGroupBalance(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	groupID := c.Params("id")
	ctx := c.UserContext()

	// Cross-tenant guard via the existing group lookup.
	group, err := h.q.GetWalletGroupByID(ctx, groupID)
	if err != nil || group.TenantID != tenantID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "wallet group not found"})
	}

	rows, err := h.q.GroupBalanceSummary(ctx, group.ID)
	if err != nil {
		return internalError(c, err)
	}
	out := make([]AssetBalanceLine, len(rows))
	for i, r := range rows {
		out[i] = AssetBalanceLine{
			Asset:       r.Asset,
			Direction:   r.Direction,
			TxCount:     r.TxCount,
			TotalAmount: r.TotalAmount,
		}
	}
	return c.JSON(GroupBalanceResponse{GroupID: group.ID, Lines: out})
}

// ──────────────────────────────────────────────────────────────────────
// GET /v1/tenants/me/statements/{month}
//   month is YYYY-MM (e.g. "2026-05"). The handler computes the half-open
//   window [first-of-month, first-of-next-month) in UTC.
// ──────────────────────────────────────────────────────────────────────

func (h *Statements) GetMonthlyStatement(c *fiber.Ctx) error {
	tenantID := middleware.TenantID(c)
	if tenantID == "" {
		return badRequest(c, "missing tenant context")
	}
	month := c.Params("month")
	start, end, err := parseMonthWindow(month)
	if err != nil {
		return badRequest(c, err.Error())
	}

	ctx := c.UserContext()

	txRows, err := h.q.TenantMonthlyStatement(ctx, sqlcgen.TenantMonthlyStatementParams{
		TenantID:    tenantID,
		CreatedAt:   pgxTs(start),
		CreatedAt_2: pgxTs(end),
	})
	if err != nil {
		return internalError(c, err)
	}
	txLines := make([]AssetBalanceLine, len(txRows))
	for i, r := range txRows {
		txLines[i] = AssetBalanceLine{
			Asset:       r.Asset,
			Direction:   r.Direction,
			TxCount:     r.TxCount,
			TotalAmount: r.TotalAmount,
		}
	}

	orRows, err := h.q.TenantMonthlyOnramp(ctx, sqlcgen.TenantMonthlyOnrampParams{
		TenantID:    tenantID,
		CreatedAt:   pgxTs(start),
		CreatedAt_2: pgxTs(end),
	})
	if err != nil {
		return internalError(c, err)
	}
	onLines := make([]OnrampLine, len(orRows))
	for i, r := range orRows {
		onLines[i] = OnrampLine{
			FiatCurrency:    r.FiatCurrency,
			Asset:           r.Asset,
			OrderCount:      r.OrderCount,
			FiatTotal:       r.FiatTotal,
			CryptoDelivered: r.CryptoDelivered,
		}
	}

	return c.JSON(MonthlyStatement{
		Month:        month,
		WindowStart:  start.UTC().Format(time.RFC3339),
		WindowEnd:    end.UTC().Format(time.RFC3339),
		Transactions: txLines,
		Onramp:       onLines,
	})
}

// ──────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────

// parseMonthWindow turns "YYYY-MM" into a UTC [start, end) window. End is
// EXCLUSIVE so SQL `created_at < end` matches without an off-by-one at
// the boundary.
func parseMonthWindow(s string) (start, end time.Time, err error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, "-")
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 2 {
		err = errors.New("month must be YYYY-MM")
		return
	}
	year, yerr := strconv.Atoi(parts[0])
	mon, merr := strconv.Atoi(parts[1])
	if yerr != nil || merr != nil || mon < 1 || mon > 12 {
		err = fmt.Errorf("month %q out of range", s)
		return
	}
	start = time.Date(year, time.Month(mon), 1, 0, 0, 0, 0, time.UTC)
	end = start.AddDate(0, 1, 0)
	return
}

// pgxTs wraps time.Time as a non-null pgtype.Timestamptz. Local helper so
// the handlers package's pgx.go doesn't grow yet another tiny shim.
func pgxTs(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
