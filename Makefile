# Atara-Pay developer Makefile.
#
# Common loops:
#   make tools          install sqlc + migrate locally
#   make sqlc           regenerate Go code from internal/db/queries
#   make migrate-up     apply all migrations (needs DATABASE_URL)
#   make migrate-down   roll back one migration
#   make migrate-new N=add_foo  scaffold a new migration pair
#   make run            build + run the gateway with .env
#   make build          static binary into bin/atara-pay
#   make test           go test ./... -race -count=1
#   make lint           go vet + gofmt check

MIGRATIONS_DIR = internal/db/migrations

.PHONY: tools
tools:
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

.PHONY: sqlc
sqlc:
	sqlc generate

.PHONY: migrate-up
migrate-up:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1

.PHONY: migrate-new
migrate-new:
	@test -n "$(N)" || (echo "usage: make migrate-new N=<short_name>" && exit 1)
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(N)

.PHONY: build
build:
	go build -o bin/atara-pay ./cmd/atara-pay

.PHONY: run
run:
	@set -a && . ./.env && set +a && go run ./cmd/atara-pay

.PHONY: test
test:
	go test ./... -race -count=1

.PHONY: lint
lint:
	go vet ./...
	@diff -u <(echo -n) <(gofmt -d ./)

# OpenAPI validation. Uses the redocly CLI via npx (no global install).
# Run from the repo root.
.PHONY: openapi-lint
openapi-lint:
	npx --yes @redocly/cli@latest lint api/v1/openapi.yaml

# Render a static HTML docs site to bin/docs.html for local preview.
.PHONY: openapi-docs
openapi-docs:
	@mkdir -p bin
	npx --yes @redocly/cli@latest build-docs api/v1/openapi.yaml -o bin/docs.html
	@echo "docs → bin/docs.html"
