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

# Integration tests need a real Postgres + Redis. smoke_test gates itself
# on DATABASE_URL / REDIS_URL so this target is safe to run when the stack
# is down — it cleanly skips. To run end-to-end:
#
#   docker compose up -d && make migrate-up && \
#     DATABASE_URL=postgres://atara:atara@localhost:5432/atara_pay?sslmode=disable \
#     REDIS_URL=redis://localhost:6379/0 \
#     make integration
.PHONY: integration
integration:
	go test -tags=integration ./tests/integration -count=1 -v

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

# ──────────────────────────────────────────────────────────────────────
# SDK generation (M12) — npx @openapitools/openapi-generator-cli
# Each target writes to sdk/<lang>/. Regenerate on every spec change.
# .openapi-generator-ignore keeps the generator from leaving boilerplate.
# ──────────────────────────────────────────────────────────────────────
SDK_DIR ?= sdk
SPEC := api/v1/openapi.yaml
OPENAPI_GEN := npx --yes @openapitools/openapi-generator-cli@latest

.PHONY: sdk-ts
sdk-ts:
	@mkdir -p $(SDK_DIR)/typescript
	$(OPENAPI_GEN) generate -i $(SPEC) -g typescript-axios -o $(SDK_DIR)/typescript \
		--additional-properties=npmName=@atara-xyz/atara-pay,supportsES6=true,withSeparateModelsAndApi=true,apiPackage=apis,modelPackage=models
	@echo "TS SDK → $(SDK_DIR)/typescript"

.PHONY: sdk-python
sdk-python:
	@mkdir -p $(SDK_DIR)/python
	$(OPENAPI_GEN) generate -i $(SPEC) -g python -o $(SDK_DIR)/python \
		--additional-properties=packageName=atara_pay,projectName=atara-pay,packageVersion=1.0.0
	@echo "Python SDK → $(SDK_DIR)/python"

.PHONY: sdk-go
sdk-go:
	@mkdir -p $(SDK_DIR)/go
	$(OPENAPI_GEN) generate -i $(SPEC) -g go -o $(SDK_DIR)/go \
		--additional-properties=packageName=atarapay,withGoMod=true,enumClassPrefix=true
	@echo "Go SDK → $(SDK_DIR)/go"

.PHONY: sdks
sdks: sdk-ts sdk-python sdk-go
