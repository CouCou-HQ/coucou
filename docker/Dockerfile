# syntax=docker/dockerfile:1
#
# coucou depends on nothing private, so there is no credential helper, no GOPRIVATE and no
# github_token secret. The final stage is scratch rather than distroless: the binary is fully
# static, so there is nothing left to carry.

# ARG before FROM is only usable in FROM. Kept in step with the `go` directive in go.mod.
ARG GO_VERSION=1.26

# ── Base: Go toolchain and shared environment ────────────────────────────────
# Pinned to BUILDPLATFORM so the toolchain always runs natively and cross-compiles via GOARCH
# below. Without this, a linux/arm64 build runs the whole Go compiler under QEMU — minutes instead
# of seconds, for a language that cross-compiles for free.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:${GO_VERSION}-alpine AS base
WORKDIR /src
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOTOOLCHAIN=local \
    GOTELEMETRY=off

# ── Deps: download modules in their own cached layer ─────────────────────────
FROM base AS deps
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# ── Builder: compile ─────────────────────────────────────────────────────────
FROM deps AS builder

# Set automatically by buildx for multi-platform builds.
ARG TARGETARCH
ENV GOARCH=${TARGETARCH} \
    GOFLAGS=-mod=readonly

# Stamped from the tag by the release workflow, so the banner an image prints on startup names the
# same build as the downloadable binary, down to the commit.
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

COPY . .

# internal/store/*/gen is committed and CI fails if regenerating it produces a diff, so this does
# not run sqlc — it would only add a toolchain download to every build.
# dave-go and modernc.org/sqlite are pure Go, so a fully static binary actually links.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /coucou ./cmd/coucou

# An empty state directory for the final stage: a scratch image has no mkdir.
RUN mkdir /state

# ── Final: nothing but the binary ────────────────────────────────────────────
FROM scratch AS final

# TLS roots for the Discord API. The zoneinfo database is embedded in the binary via time/tzdata,
# so quiet hours work with no files in the image and there is nothing else to copy.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /coucou /coucou
COPY docker/config.toml /etc/coucou/config.toml
# Owned by the runtime user so a named volume mounted here inherits it and SQLite can write.
COPY --from=builder --chown=65534:65534 /state /var/lib/coucou

USER 65534:65534

# No HEALTHCHECK: a scratch image has no shell and no curl, and the binary no longer probes itself.
# /readyz is still served — point an orchestrator's HTTP probe at it.

ENTRYPOINT ["/coucou"]
