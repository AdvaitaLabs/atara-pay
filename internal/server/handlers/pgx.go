package handlers

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgxText wraps a non-empty string in a pgtype.Text whose Valid bit is set.
// Empty input produces a SQL NULL.
func pgxText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// pgxInt2 wraps a small int in a pgtype.Int2 with Valid=true. Used for
// nullable SMALLINT columns like wallets.key_version, which only carries a
// value for self-custodied (Tempo) wallets.
func pgxInt2(v int16) pgtype.Int2 {
	return pgtype.Int2{Int16: v, Valid: true}
}

// pgxTimestamptz wraps a time.Time into a pgtype.Timestamptz with
// Valid=true. The zero time becomes SQL NULL — useful for nullable
// expires_at / revoked_at columns.
func pgxTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}
