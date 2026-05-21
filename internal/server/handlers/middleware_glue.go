package handlers

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atara-xyz/atara-pay/internal/db/sqlcgen"
)

// NewQueriesForMiddleware returns a sqlcgen.Querier bound to the same pool
// used by the auth handlers. The server's auth middleware needs only a tiny
// subset of the Querier (GetAPIKeyByHash, TouchAPIKeyUsage), but accepting
// the full *Queries here keeps the dependency surface honest — and avoids a
// second connection pool.
func NewQueriesForMiddleware(pool *pgxpool.Pool) *sqlcgen.Queries {
	return sqlcgen.New(pool)
}
