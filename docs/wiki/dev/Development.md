# Development

## Build and run

Requires Go 1.26 (see [NOTES.md](https://github.com/be-sandaa/coucou/blob/main/NOTES.md) — the pinned dependency set forces it).

```sh
make build          # vet + test, then CGO_ENABLED=0 static binary into bin/coucou
make build-fast     # skip the checks
make test           # both store backends; add TEST_DATABASE_URL for the Postgres half
make lint           # go vet + golangci-lint
make gen            # regenerate internal/store/{pg,sqlite}/gen — commit the result
make image          # Podman build of the scratch image
make help           # everything else
```

Running it:

```sh
./bin/coucou -config config.toml   # migrate, register the slash commands, load settings, watch the sounds dir, connect
./bin/coucou                       # installed: reads /etc/coucou/config.toml
```

Migrations, command registration and the version stamp are not separate modes: the bot applies its
migrations and registers its commands on the way up, and prints the build it came from in the
banner once Discord answers.

Or via make: `make dev`, which runs the working tree as a development build — its commands go to
`DISCORD_DEV_GUILD` rather than to every server the bot is in. `make run SERVICE=coucou TAGS=dev`
is the same thing the long way round.

## Building the image

```sh
make image                                            # or:
docker build -t coucou -f docker/Dockerfile .
docker run --rm -e DATABASE_URL=... -e DISCORD_BOT_TOKEN=... -v ./profile:/var/lib/coucou/profile:ro coucou
```

`FROM scratch`, non-root (65534), ~31 MiB. The zoneinfo database is embedded in the binary via
`time/tzdata`, so quiet hours work with no files in the image and there is nothing to copy but the
binary and the TLS roots. Running it is in [Installation](Installation#container-image).

## Provenance

Started from a Go service template. The build plumbing (`makefile`, CI, lint config) still follows
it, and coucou's own make targets live in `makefile.local` so a sync from the template has nothing of
ours to overwrite.

[NOTES.md](https://github.com/be-sandaa/coucou/blob/main/NOTES.md) also records every library API mismatch found while getting this to compile,
and why each was resolved the way it was.
