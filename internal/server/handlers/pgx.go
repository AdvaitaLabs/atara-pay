package handlers

import "github.com/jackc/pgx/v5/pgtype"

// pgxText wraps a non-empty string in a pgtype.Text whose Valid bit is set.
// Empty input produces a SQL NULL.
func pgxText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
