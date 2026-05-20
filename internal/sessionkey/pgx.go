package sessionkey

import (
	"encoding/json"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/atara-xyz/atara-pay/internal/adapters/tempo"
)

// commonPathUSD wraps the pathUSD precompile address in the common.Address
// type used by tempo.AllowedCall.Target. Kept local so callers don't import
// go-ethereum directly.
func commonPathUSD() common.Address {
	return common.HexToAddress(tempo.PathUSDAddress)
}

// pgx column constructors. Each mirrors the handlers package's helpers but
// is kept local so this package has zero handlers imports — sessionkey is
// the service the handlers will call, not the other way round.

func pgxOptText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func pgxOptTimestamp(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// pgxRequiredTimestamp differs from pgxOptTimestamp in name only — used at
// call sites where the column is NOT NULL (session_keys.expires_at) so
// future readers can tell the column is mandatory at a glance.
func pgxRequiredTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// pgxTickTimestamp is the rotator's "now" cutoff parameter. Same shape as
// pgxRequiredTimestamp; named separately so the call site reads as a
// boundary timestamp, not a record field.
func pgxTickTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// pgxIntUSD wraps a whole-USD int64 into a Valid pgtype.Numeric at exp=0.
// Mirror of limits.ToNumeric, kept local to avoid the import.
func pgxIntUSD(v int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(v), Exp: 0, Valid: true}
}

// marshalStringList serializes []string as JSON for jsonb columns. Empty
// slices become "[]" (not "null") so the matching DB CHECK / JSONB ops
// behave correctly.
func marshalStringList(xs []string) []byte {
	if len(xs) == 0 {
		return []byte("[]")
	}
	out, err := json.Marshal(xs)
	if err != nil {
		return []byte("[]")
	}
	return out
}
