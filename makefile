PACKAGE  := $(notdir $(CURDIR))
BINDIR   := bin

# Per-service additions — targets and variable defaults the template does not
# ship, so a template sync has nothing to overwrite. Read here, before every
# ?= below, so anything it sets wins.
-include makefile.local

# ── Version ──────────────────────────────────────────────────────────────────
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

# ── Service discovery ─────────────────────────────────────────────────────────
# Discover by the Go files inside each cmd/<name>/, not by `wildcard cmd/*/`.
# The trailing slash only filters to directories on some GNU make builds — on the
# CI runner's it does not, and cmd/README.md was picked up as a binary to build.
SERVICES := $(sort $(notdir $(patsubst %/,%,$(dir $(wildcard cmd/*/*.go)))))

# ── Go environment — all overridable on the command line ──────────────────────
#
#   CGO=1                Enable CGO (default: 0)
#   GOOS=linux           Target OS for cross-compilation
#   GOARCH=arm64         Target architecture for cross-compilation
#   RACE=1               Enable race detector (automatically sets CGO=1)
#   TAGS=tag1,tag2       Build tags (default: empty)
#   LDFLAGS="-X ..."     Extra linker flags appended after -s -w
#                          e.g. LDFLAGS="-X main.version=$(VERSION) -X main.commit=$(COMMIT)"
#   GCFLAGS="..."        Compiler flags e.g. "all=-N -l" disables optimisations for dlv
#   GOPRIVATE=...        Private module prefix — also sets GONOSUMDB (default: none)
#
CGO       ?= 0
GOOS      ?=
GOARCH    ?=
RACE      ?= 0
TAGS      ?=
LDFLAGS   ?=
GCFLAGS   ?=
GOPRIVATE ?=
# coucou has no private module dependencies; everything it imports is public.

# Race detector requires CGO
ifeq ($(RACE),1)
CGO := 1
endif

export CGO_ENABLED=$(CGO)
export GOTOOLCHAIN=local
export GOTELEMETRY=off
export GOPRIVATE
export GONOSUMDB=$(GOPRIVATE)

ifneq ($(GOOS),)
export GOOS
endif
ifneq ($(GOARCH),)
export GOARCH
endif

# ── Assembled build flags (recursively expanded so overrides apply at use time)
_LD_RELEASE := -s -w
_BUILD_FLAGS  = -trimpath -ldflags="$(_LD_RELEASE) $(LDFLAGS)"
ifneq ($(TAGS),)
_BUILD_FLAGS += -tags "$(TAGS)"
endif
ifneq ($(GCFLAGS),)
_BUILD_FLAGS += -gcflags="$(GCFLAGS)"
endif
ifeq ($(RACE),1)
_BUILD_FLAGS += -race
endif

# ── Proto / buf ───────────────────────────────────────────────────────────────
PROTO_DIR        := proto
BREAKING_AGAINST ?= .git#branch=main,subdir=proto
# Guarded: this repo ships no proto/ dir, and an unguarded find prints an error on every target.
PROTO_FILES      := $(shell [ -d $(PROTO_DIR) ] && find $(PROTO_DIR) -type f -name '*.proto')

# ── Tool runners — Docker by default for a reproducible environment ───────────
#
# Both tools only need config files + source — no Go module resolution required.
# Override with a local binary when needed:
#
#   BUF=buf make generate
#   SQLC=sqlc make sqlc
#
_UID       := $(shell id -u)
_GID       := $(shell id -g)
_BUF_CACHE := $(HOME)/.cache/buf
# Keep in step with .github/workflows/lint-and-test.yml — CI running an older
# buf than `make generate` is how a valid proto fails lint only on the runner.
BUF_VERSION := 1.72.0

BUF  ?= docker run --rm \
	-v "$(PWD):/workspace" -w /workspace \
	-v "$(_BUF_CACHE):/tmp/.cache/buf" \
	-e HOME=/tmp \
	--user $(_UID):$(_GID) \
	bufbuild/buf:$(BUF_VERSION)
SQLC ?= docker run --rm -v "$(PWD):/src"       -w /src       --user $(_UID):$(_GID) sqlc/sqlc

# ─────────────────────────────────────────────────────────────────────────────

.DEFAULT_GOAL := help

.PHONY: build build-fast build-debug build-race clean \
        lint fmt vet test test-cover test-integration verify benchmark breaking \
        generate mock sqlc run dev tidy \
        check-tools install-tools update-deps env \
        help

## 🧱 Build

build: verify ## Build all services — runs vet + tests first
	@for svc in $(SERVICES); do \
		echo "Building $$svc ..."; \
		go build $(_BUILD_FLAGS) -o $(BINDIR)/$$svc ./cmd/$$svc; \
	done

build-fast: ## Build all services without running checks
	@for svc in $(SERVICES); do \
		echo "Building $$svc ..."; \
		go build $(_BUILD_FLAGS) -o $(BINDIR)/$$svc ./cmd/$$svc; \
	done

build-debug: ## Build with debug symbols — disables stripping and inlining (for delve)
	@$(MAKE) build-fast _LD_RELEASE="" GCFLAGS="all=-N -l"

build-race: ## Build with race detector enabled (implies CGO=1)
	@$(MAKE) build-fast RACE=1

clean: ## Remove compiled binaries and all generated artifacts
	@rm -rf $(BINDIR)
	@if [ -f ".mockery.yaml" ]; then \
		find . -path './mocks' -type d -exec rm -rf {} + 2>/dev/null || true; \
	fi
	@if [ -n "$(PROTO_FILES)" ]; then \
		find gen/pb -type f \( -name '*.pb.go' -o -name '*_grpc.pb.go' -o -name '*.pb.gw.go' \) -delete 2>/dev/null || true; \
		find gen/pb -mindepth 1 -type d -empty -delete 2>/dev/null || true; \
		find docs/openapi -type f -name '*.yaml' -delete 2>/dev/null || true; \
		find docs/openapi -mindepth 1 -type d -empty -delete 2>/dev/null || true; \
	fi

## 🧹 Linting & Formatting

lint: ## Lint everything (go vet, golangci-lint, proto — skips sections whose configs are absent)
	@echo "==> go vet"
	@go vet ./...
	@echo "==> golangci-lint"
	@golangci-lint run $(if $(wildcard .golangci.override.yml),--config .golangci.override.yml,--config .golangci.yml) ./...
	@if [ -n "$(PROTO_FILES)" ]; then \
		echo "==> proto (buf lint)"; \
		$(BUF) lint; \
	fi

fmt: ## Format and simplify all Go files
	@gofmt -s -w .

vet: ## Run go vet (fast standalone check, also run as part of lint and verify)
	@go vet ./...

## 🧪 Testing

# The recipes below hardcode -race, which needs cgo — and this makefile exports
# CGO_ENABLED=$(CGO), 0 by default, so without this override every one of them
# dies on "-race requires cgo" before running a test.
_RACE := CGO_ENABLED=1

# Extra prerequisites for the integration suite — a broker, a fake, anything
# testcontainers does not start per-run. A service sets it in makefile.local.
INTEGRATION_DEPS ?=

test: ## Run all unit tests with race detector
	@$(_RACE) go test -race -count=1 -v ./...

test-cover: ## Run tests and generate HTML coverage report
	@$(_RACE) go test -race -count=1 -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-integration: $(INTEGRATION_DEPS) ## Run integration tests (requires Docker)
	@$(_RACE) go test -tags=integration -race -count=1 -v ./test/integration/...

verify: vet test ## Run vet and tests

benchmark: ## Run benchmarks
	@go test -bench=. -benchmem ./...

breaking: ## Check for breaking API changes against main branch
	@if [ -z "$(PROTO_FILES)" ]; then echo "No .proto files found — nothing to check."; exit 0; fi
	@$(BUF) breaking --against "$(BREAKING_AGAINST)"

## ⚙️  Dev

# Run a service locally.
#   SERVICE=api      Which service to run (required)
#   ARGS="..."       Arguments forwarded to the binary
#   ENV="KEY=val"    Extra environment variables (space-separated)
#   RACE=1           Run with race detector
#
# Examples:
#   make run SERVICE=api
#   make run SERVICE=api ARGS="--port 8080"
#   make run SERVICE=api ENV="LOG_LEVEL=debug DATABASE_URL=postgres://localhost/dev"
#   make run SERVICE=api RACE=1
run: ## Run a service (usage: make run SERVICE=api [ARGS="..."] [ENV="KEY=val ..."])
	@if [ -z "$(SERVICE)" ]; then \
		echo "Error: SERVICE is required. Available: $(SERVICES)"; \
		exit 1; \
	fi
	@env $(ENV) go run $(if $(TAGS),-tags "$(TAGS)") $(if $(filter 1,$(RACE)),-race) ./cmd/$(SERVICE) $(ARGS)

tidy: ## Tidy and verify go modules
	@go mod tidy
	@go mod verify

# -tags dev, always: a release build registers its commands globally on every start, and running
# the working tree against every server the bot is in is the failure the build split prevents.
dev: ## Run the bot from source (development build — commands go to DISCORD_DEV_GUILD)
	@go run -tags dev ./cmd/coucou

## 🔧 Code Generation

generate: ## Generate all artifacts (go generate, mocks, sqlc, proto — skips sections whose configs are absent)
	@echo "==> go generate"
	@go generate ./...
	@if [ -f ".mockery.yaml" ]; then \
		echo "==> mocks (mockery)"; \
		which mockery > /dev/null 2>&1 || (echo "Error: mockery not found. Run: make install-tools"; exit 1); \
		mockery; \
	fi
	@if [ -f "sqlc.yaml" ]; then \
		echo "==> sqlc"; \
		$(SQLC) generate; \
	fi
	@if [ -n "$(PROTO_FILES)" ]; then \
		mkdir -p "$(_BUF_CACHE)"; \
		echo "==> proto (buf dep update)"; \
		$(BUF) dep update; \
		echo "==> proto (buf generate)"; \
		find gen/pb -type f \( -name '*.pb.go' -o -name '*_grpc.pb.go' -o -name '*.pb.gw.go' \) -delete 2>/dev/null || true; \
		find gen/pb -mindepth 1 -type d -empty -delete 2>/dev/null || true; \
		find docs/openapi -type f -name '*.yaml' -delete 2>/dev/null || true; \
		find docs/openapi -mindepth 1 -type d -empty -delete 2>/dev/null || true; \
		$(BUF) generate; \
	fi

mock: ## Generate mocks with mockery
	@which mockery > /dev/null 2>&1 || (echo "Error: mockery not found. Run: make install-tools"; exit 1)
	@mockery

sqlc: ## Generate type-safe SQL code
	@$(SQLC) generate

## 🛠️  Tooling

check-tools: ## Verify all required tools are installed
	@which go            > /dev/null 2>&1 || (echo "Error: go is not installed";           exit 1)
	@which docker        > /dev/null 2>&1 || (echo "Error: docker not found (required for sqlc and buf)"; exit 1)
	@which golangci-lint > /dev/null 2>&1 || (echo "Error: golangci-lint not found. Install: https://golangci-lint.run/usage/install/"; exit 1)
	@which mockery       > /dev/null 2>&1 || echo "Warning: mockery not found (mocks).     Run: make install-tools"
	@go version
	@golangci-lint version
	@docker version --format "Docker {{.Client.Version}}"
	@echo "All required tools OK."

install-tools: ## Install all Go-managed tools (sqlc and buf run via Docker — no install needed)
	@echo "==> core"
	@go install github.com/vektra/mockery/v2@latest
	@echo "Done. Run 'make check-tools' to verify."

update-deps: ## Update all dependencies (Go modules, buf lock + IDE vendor — skips sections whose dirs are absent)
	@echo "==> go modules"
	@go get -u ./...
	@go mod tidy
	@go mod verify
	@if [ -d "$(PROTO_DIR)" ]; then \
		echo "==> buf dependencies"; \
		$(BUF) dep update; \
		$(BUF) export $(PROTO_DIR) --output $(PROTO_DIR)/vendor/proto; \
	fi

env: ## Show current build environment
	@echo ""
	@echo "  PACKAGE      = $(PACKAGE)"
	@echo "  SERVICES     = $(SERVICES)"
	@echo "  VERSION      = $(VERSION)"
	@echo "  COMMIT       = $(COMMIT)"
	@echo "  BINDIR       = $(BINDIR)"
	@echo ""
	@echo "  CGO          = $(CGO)   (CGO=1 to enable)"
	@echo "  GOOS         = $(if $(GOOS),$(GOOS),(host))"
	@echo "  GOARCH       = $(if $(GOARCH),$(GOARCH),(host))"
	@echo "  RACE         = $(RACE)   (RACE=1 to enable)"
	@echo "  GOTOOLCHAIN  = local"
	@echo "  GOTELEMETRY  = off"
	@echo "  GOPRIVATE    = $(GOPRIVATE)"
	@echo "  GONOSUMDB    = $(GOPRIVATE)"
	@echo "  TAGS         = $(if $(TAGS),$(TAGS),(none))"
	@echo "  LDFLAGS      = $(if $(LDFLAGS),$(LDFLAGS),(none, appended after -s -w))"
	@echo "  GCFLAGS      = $(if $(GCFLAGS),$(GCFLAGS),(none))"
	@echo ""

## ℹ️  Help

help: ## Show this help
	@echo ""
	@echo "Available commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "  Services: $(SERVICES)"
	@echo "  Version:  $(VERSION) ($(COMMIT))"
	@echo ""
	@echo "  Override build env: CGO GOOS GOARCH RACE TAGS LDFLAGS GCFLAGS GOPRIVATE"
	@echo "  e.g. make build GOOS=linux GOARCH=arm64"
