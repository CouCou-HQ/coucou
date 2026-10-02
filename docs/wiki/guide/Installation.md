# Installation

coucou is one static binary. CGO is off and the timezone database is embedded, so the binary is the
entire dependency. Pick whichever way of running it you already use:

| Way | Good for | Database |
|---|---|---|
| [Release binary](#release-binary) | a single box, trying it out | SQLite file, or PostgreSQL |
| [Container image](#container-image) | Docker or Podman | SQLite on a volume, or PostgreSQL |
| [Docker Compose](#docker-compose) | one host, everything included | PostgreSQL 17, bundled |
| [Helm chart](#helm-chart) | a Kubernetes cluster you already run | PostgreSQL you provide |
| [From source](#from-source) | hacking on it | either |

Whichever you pick, you also need a Discord application and a folder of sounds. Both are covered in
[Running the bot](Running-the-bot).

## Release binary

Every `v*` tag publishes static binaries for linux and macOS, amd64 and arm64. Swap `linux_amd64`
for `linux_arm64`, `darwin_amd64` or `darwin_arm64` as needed:

```sh
tag=v0.10.0
base=https://github.com/be-sandaa/coucou/releases/download/$tag
curl -fsSLO "$base/coucou_${tag}_linux_amd64.tar.gz"
curl -fsSLO "$base/coucou_${tag}_SHA256SUMS"
sha256sum --check --ignore-missing "coucou_${tag}_SHA256SUMS"
tar -xzf "coucou_${tag}_linux_amd64.tar.gz"
./coucou -h
```

The release binary carries both database backends, so it runs against a plain SQLite file with no
database server at all.

## Container image

The same tag publishes a multi-arch image for `linux/amd64` and `linux/arm64`. Image tags drop the
leading `v`, so the git tag `v0.10.0` is the image tag `0.10.0`:

```sh
docker pull ghcr.io/be-sandaa/coucou:0.10.0
docker run --rm \
  -e DATABASE_URL=sqlite:///data/coucou.db \
  -e DISCORD_BOT_TOKEN=... \
  -v coucou-data:/data \
  -v "$PWD/sounds:/sounds:ro" \
  ghcr.io/be-sandaa/coucou:0.10.0
```

The image is `FROM scratch` and runs as uid 65534, so the sound files must be world-readable. It
has no shell and no `HEALTHCHECK`; health is on the bot's own HTTP port (see
[Running the bot](Running-the-bot#is-it-working)).

## Docker Compose

`deployment/compose/` runs the bot and a PostgreSQL 17 container together:

```sh
cd deployment/compose
cp .env.example .env     # fill in DISCORD_BOT_TOKEN and POSTGRES_PASSWORD
docker compose up -d --build
```

`--build` compiles the image from your checkout. To run a published one instead, set
`COUCOU_IMAGE=ghcr.io/be-sandaa/coucou:0.10.0` in `.env` and drop the flag.

## Helm chart

The chart is published as an OCI artifact next to the image, at the same version. It ships no
database on purpose: point it at a PostgreSQL you already run.

```sh
kubectl create secret generic coucou-creds \
  --from-literal=token='<bot token>' \
  --from-literal=url='postgres://coucou:<password>@<host>:5432/coucou'

helm install coucou oci://ghcr.io/be-sandaa/charts/coucou --version 0.10.0 \
  --set discord.existingSecret=coucou-creds \
  --set database.existingSecret=coucou-creds \
  --set sounds.existingClaim=coucou-sounds
```

The chart reads the keys `token` and `url` by default, and refuses to render without both. Sounds
come from a PVC (`sounds.existingClaim`) or, on a single-node cluster, a `sounds.hostPath`.

Run **one replica, always**. The bot holds a Discord gateway connection, so a second instance is a
second bot: it joins the same channels, talks over the first, and doubles every stats row. The chart
pins `replicas: 1` with a `Recreate` strategy for that reason.

## From source

Requires Go 1.26.

```sh
git clone https://github.com/be-sandaa/coucou
cd coucou
make build          # vet + test, then a static binary in bin/coucou
./bin/coucou -h
```

`make help` lists everything else. `make dev` runs a development build whose commands go to one
test server (`DISCORD_DEV_GUILD`) instead of every server the bot is in.

**Next:** [Running the bot](Running-the-bot).
