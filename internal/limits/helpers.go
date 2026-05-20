package limits

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgxTextOpt is a local mirror of handlers.pgxText — empty string becomes
// SQL NULL, anything else a Valid pgtype.Text. Duplicated rather than
// imported so the limits package stays standalone.
func pgxTextOpt(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// numericToString reduces a pgtype.Numeric or already-string column to its
// canonical decimal representation. The sqlc override emits NOT NULL
// NUMERIC as Go string and nullable NUMERIC as pgtype.Numeric — this helper
// lets us treat both uniformly at the call site.
func numericToString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case pgtype.Numeric:
		if !x.Valid {
			return ""
		}
		// pgtype.Numeric stringifies via MarshalJSON. Strip quotes.
		b, err := x.MarshalJSON()
		if err != nil {
			return ""
		}
		s := string(b)
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		return s
	default:
		return ""
	}
}

// parseJSONStringList unmarshals a JSON array column ([]byte from sqlc) to
// a flat []string. Bad input or non-arrays yield an empty slice rather
// than an error — the column is allowed to be empty or "[]".
func parseJSONStringList(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
