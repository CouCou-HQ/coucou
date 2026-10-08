# Installation

coucou is one static binary. CGO is off and the timezone database is embedded, so the binary is the
entire dependency. Pick whichever way of running it you already use:

| Way | Good for | Database |
|---|---|---|
| [Release binary](#release-binary) | a single box, trying it out | SQLite file, or PostgreSQL |
| [As a service](#install-as-a-service) | a single box, kept running by systemd | SQLite file, or PostgreSQL |
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
tag=v0.4.1
base=https://github.com/be-sandaa/coucou/releases/download/$tag
curl -fsSLO "$base/coucou_${tag}_linux_amd64.tar.gz"
curl -fsSLO "$base/coucou_${tag}_SHA256SUMS"
sha256sum --check --ignore-missing "coucou_${tag}_SHA256SUMS"
tar -xzf "coucou_${tag}_linux_amd64.tar.gz"
./coucou -h
```

The release binary carries both database backends, so it runs against a plain SQLite file with no
database server at all.

## Install as a service

`deployment/systemd/coucou.service` runs the release binary under systemd with a throwaway
`DynamicUser=` and `/var/lib/coucou` as its only writable directory. As root, from the unpacked
tarball and a checkout:

```sh
install -m 0755 coucou /usr/local/bin/coucou
install -D -m 0644 config.example.toml /etc/coucou/config.toml
install -m 0600 /dev/null /etc/coucou/env
install -m 0644 deployment/systemd/coucou.service /etc/systemd/system/
```

| File | Mode | Holds |
|---|---|---|
| `/etc/coucou/config.toml` | `0644` | settings, no secrets; it references `${DISCORD_BOT_TOKEN}` and `${DATABASE_URL}` |
| `/etc/coucou/env` | `0600 root` | the secrets, as `KEY=value` lines |
| `/var/lib/coucou/profile/` | world-readable | `profile.toml` and `sounds/` |

```sh
# /etc/coucou/env
DISCORD_BOT_TOKEN=...
DATABASE_URL=sqlite:///var/lib/coucou/coucou.db
```

systemd reads the env file as root and hands the bot only the variables, so the bot can't read the
file itself.

Put the profile in place **before the first start**:

```sh
install -d -m 0755 /var/lib/coucou/profile/sounds
install -m 0644 profile.toml /var/lib/coucou/profile/
install -m 0644 sounds/*.ogg /var/lib/coucou/profile/sounds/
systemctl daemon-reload
systemctl enable --now coucou
journalctl -u coucou -f
```

On that start systemd moves `/var/lib/coucou` to `/var/lib/private/coucou`, leaves a symlink in its
place, and hands the tree to the dynamic user. Keep using `/var/lib/coucou/profile` as root after
that, but files added later keep the owner you give them, so make them world-readable (`0644`, and
`0755` for directories) or the bot can't open them.

## Container image

The same tag publishes a multi-arch image for `linux/amd64` and `linux/arm64`. Image tags drop the
leading `v`, so the git tag `v0.4.1` is the image tag `0.4.1`:

```sh
docker pull ghcr.io/be-sandaa/coucou:0.4.1
docker run --rm \
  -e DATABASE_URL=sqlite:///var/lib/coucou/coucou.db \
  -e DISCORD_BOT_TOKEN=... \
  -v coucou-data:/var/lib/coucou \
  -v "$PWD/profile:/var/lib/coucou/profile:ro" \
  ghcr.io/be-sandaa/coucou:0.4.1
```

The image carries a minimal config at the default path, `/etc/coucou/config.toml` (from
[`docker/config.toml`](https://github.com/be-sandaa/coucou/blob/main/docker/config.toml)): token and
database from the environment, the profile at `/var/lib/coucou/profile`. Mount your own file over it
for anything else. `/var/lib/coucou` belongs to the runtime user, so a volume there can hold the
SQLite database too. Set `GOMEMLIMIT=48MiB`.

The image is `FROM scratch` and runs as uid 65534, so the profile and its sounds must be world-readable. It
has no shell and no `HEALTHCHECK`; health is on the bot's own HTTP port (see
[Running the bot](Running-the-bot#is-it-working)).

## Docker Compose

`deployment/compose/` runs the bot and a PostgreSQL 17 container together:

```sh
cd deployment/compose
cp .env.example .env     # fill in DISCORD_BOT_TOKEN and POSTGRES_PASSWORD
cp config.example.toml config.toml   # bot settings; the defaults run as they are
docker compose up -d --build
```

`--build` compiles the image from your checkout. To run a published one instead, set
`COUCOU_IMAGE=ghcr.io/be-sandaa/coucou:0.4.1` in `.env` and drop the flag.

## Helm chart

The chart is published as an OCI artifact next to the image, at the same version. It ships no
database on purpose: point it at a PostgreSQL you already run.

```sh
kubectl create secret generic coucou-creds \
  --from-literal=token='<bot token>' \
  --from-literal=url='postgres://coucou:<password>@<host>:5432/coucou'

helm install coucou oci://ghcr.io/be-sandaa/charts/coucou --version 0.4.1 \
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
