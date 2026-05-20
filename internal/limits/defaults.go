package limits

import (
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"
)

// Default tier presets. See PLAN.md §1.1.
//
//	Conservative — every new tenant lands here on signup.
//	Standard     — opt-in after KYB completes.
//	Aggressive   — opt-in after signed risk waiver + financial statement.
//
// All caps are USDC-denominated. Per-tx is the hard ceiling on any single
// transfer; daily / weekly / monthly are the rolling-window accumulators.
//
// Values are expressed as `int64` whole-USD; the helpers below convert to
// pgtype.Numeric at the schema's NUMERIC(38, 18) shape.
type Tier struct {
	Name           string
	PerTxUSD       int64
	DailyUSD       int64
	WeeklyUSD      int64
	MonthlyUSD     int64
	SessionTTLDays int
}

// Atara's three tiers.
var (
	TierConservative = Tier{
		Name: "conservative",
		PerTxUSD: 5, DailyUSD: 20, WeeklyUSD: 100, MonthlyUSD: 300,
		SessionTTLDays: 1,
	}
	TierStandard = Tier{
		Name: "standard",
		PerTxUSD: 50, DailyUSD: 200, WeeklyUSD: 1000, MonthlyUSD: 3000,
		SessionTTLDays: 7,
	}
	TierAggressive = Tier{
		Name: "aggressive",
		PerTxUSD: 500, DailyUSD: 5000, WeeklyUSD: 30000, MonthlyUSD: 100000,
		SessionTTLDays: 30,
	}
)

// ToNumeric returns a pgtype.Numeric carrying `v` (in whole USDC) at exp=0,
// Valid=true. Used to populate limit_policies INSERT params.
func ToNumeric(v int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(v), Exp: 0, Valid: true}
}
