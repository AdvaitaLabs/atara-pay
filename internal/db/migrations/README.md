# Database Migrations

Files are numbered `NNNNNN_<name>.up.sql` and `NNNNNN_<name>.down.sql`,
compatible with [golang-migrate](https://github.com/golang-migrate/migrate).

Apply locally:

```sh
# Install once:
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Apply:
migrate -path internal/db/migrations \
        -database "$DATABASE_URL" up

# Roll back one step:
migrate -path internal/db/migrations \
        -database "$DATABASE_URL" down 1
```

## Conventions

- Every `*.up.sql` has a matching `*.down.sql`
- IDs are ULIDs stored as TEXT (sortable, human-readable, app-generated)
- `created_at` and `updated_at` are `TIMESTAMPTZ NOT NULL DEFAULT NOW()`
- `updated_at` is auto-bumped via the `touch_updated_at()` trigger
- Money amounts use `NUMERIC(38, 18)` — wide enough for sats + wei
- Tenant isolation: every customer-data table has `tenant_id` FK with
  `ON DELETE CASCADE`
