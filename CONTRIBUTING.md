# Contributing

## Branching

| Branch | Purpose |
|--------|---------|
| `main` | Production-ready; protected |
| `develop` | Integration branch for in-progress work |
| `feat/<name>` | New feature; PR targets `develop` |
| `fix/<name>` | Bug fix; PR targets `develop` |
| `chore/<name>` | Tooling / maintenance |

## Commit Convention

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short description>
```

Types: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `chore`, `ci`

## Local Setup

1. Install Go 1.24+
2. Copy and configure environment: `cp .env.example .env`
3. Start the dev stack: `docker compose up -d postgres otel-collector jaeger prometheus grafana`
4. Install tools: `make check-tools`
5. Install pre-commit hooks: `pre-commit install`
6. Run with hot-reload: `make dev`

## Before Opening a PR

```bash
make fmt            # Format code
make lint           # Run all linters
make test           # Run unit tests
make vet            # Run go vet
make proto-lint     # Lint proto files (if changed)
```

## Database Migrations

- Create: `make migrate-create NAME=<description>`
- Apply: `make migrate-up`
- All migrations **must be reversible** — always write the `+goose Down` block.

## Proto Changes

1. Add or modify `.proto` files under `proto/`
2. Regenerate artifacts: `make proto-generate`
3. Lint: `make proto-lint`
4. Check for breaking changes: `make proto-breaking`
5. Commit the generated `gen/` files alongside your `.proto` changes.

## Pull Request Checklist

- [ ] Tests added for new behaviour
- [ ] Migrations are reversible
- [ ] Proto changes are non-breaking (or intentionally versioned)
- [ ] Observability wired (tracing spans, structured log fields)
- [ ] `.env.example` updated for any new environment variables
